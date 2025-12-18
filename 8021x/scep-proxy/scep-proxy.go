package main

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"

	"github.com/lf-edge/eve-api/go/auth"
	"github.com/lf-edge/eve-api/go/evecommon"
	"github.com/lf-edge/eve-api/go/proxy"
)

const (
	maxPayloadSize  = 2 << 20 // 2MB
	hashSha256Len16 = 16
	hashSha256Len32 = 32
)

type scepProxy struct {
	listenAddr string
	tlsCert    string
	tlsKey     string
	tlsCACert  string

	clientCertPath string
	proxyCertPath  string
	proxyKeyPath   string

	// Loaded certificates and keys
	clientCert *x509.Certificate
	proxyCert  *x509.Certificate
	proxyKey   *ecdsa.PrivateKey

	// Cached hashes
	clientCertHash []byte
	proxyCertHash  []byte
}

func main() {
	var (
		flListenAddr = flag.String("listen", ":443", "address to listen on")
		flTLSCert    = flag.String("tls-cert", "", "path to TLS certificate for HTTPS server")
		flTLSKey     = flag.String("tls-key", "", "path to TLS private key for HTTPS server")
		flTLSCACert  = flag.String("tls-ca-cert", "", "path to TLS CA certificate (optional, will be concatenated with server cert)")
		flClientCert = flag.String("client-cert", "", "path to client certificate (for verifying client signatures)")
		flProxyCert  = flag.String("proxy-cert", "", "path to proxy certificate (for signing responses)")
		flProxyKey   = flag.String("proxy-key", "", "path to proxy ECDSA private key (for signing responses)")
	)

	flag.Parse()

	if *flTLSCert == "" || *flTLSKey == "" {
		log.Fatal("TLS certificate and key are required")
	}
	if *flClientCert == "" {
		log.Fatal("Client certificate is required")
	}
	if *flProxyCert == "" || *flProxyKey == "" {
		log.Fatal("Proxy certificate and key are required")
	}

	proxy := &scepProxy{
		listenAddr:     *flListenAddr,
		tlsCert:        *flTLSCert,
		tlsKey:         *flTLSKey,
		tlsCACert:      *flTLSCACert,
		clientCertPath: *flClientCert,
		proxyCertPath:  *flProxyCert,
		proxyKeyPath:   *flProxyKey,
	}

	if err := proxy.run(); err != nil {
		log.Fatal(err)
	}
}

func (p *scepProxy) run() error {
	if err := p.loadCertificates(); err != nil {
		return fmt.Errorf("failed to load certificates: %v", err)
	}

	// Prepare TLS certificate chain
	tlsCertFile, cleanup, err := p.prepareTLSCertChain()
	if err != nil {
		return fmt.Errorf("failed to prepare TLS certificate chain: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	http.HandleFunc("/proxy/scep", p.handleSCEPProxy)

	log.Printf("Running SCEP proxy server on %s", p.listenAddr)
	err = http.ListenAndServeTLS(p.listenAddr, tlsCertFile, p.tlsKey, nil)
	if err != nil {
		return fmt.Errorf("HTTP server failed: %v", err)
	}
	return nil
}

func (p *scepProxy) loadCertificates() error {
	var err error

	// Load client certificate (for verification)
	p.clientCert, err = loadPEMCertFromFile(p.clientCertPath)
	if err != nil {
		return fmt.Errorf("failed to load client cert: %v", err)
	}
	p.clientCertHash = computeSha(p.clientCert.Raw)

	// Load proxy certificate (for signing)
	p.proxyCert, err = loadPEMCertFromFile(p.proxyCertPath)
	if err != nil {
		return fmt.Errorf("failed to load proxy cert: %v", err)
	}
	p.proxyCertHash = computeSha(p.proxyCert.Raw)

	// Load proxy key (for signing)
	p.proxyKey, err = loadECDSAKeyFromFile(p.proxyKeyPath)
	if err != nil {
		return fmt.Errorf("failed to load proxy key: %v", err)
	}
	return nil
}

// prepareTLSCertChain creates a certificate chain file if a CA cert is provided.
// If tlsCACert is specified, it concatenates server cert + CA cert into a temporary file.
// Returns the path to use for TLS and an optional cleanup function.
func (p *scepProxy) prepareTLSCertChain() (certPath string, cleanup func(), err error) {
	// If no CA cert specified, use the server cert as-is
	if p.tlsCACert == "" {
		return p.tlsCert, nil, nil
	}

	// Read server certificate
	serverCertPEM, err := ioutil.ReadFile(p.tlsCert)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read TLS cert: %v", err)
	}

	// Read CA certificate
	caCertPEM, err := ioutil.ReadFile(p.tlsCACert)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read TLS CA cert: %v", err)
	}

	// Create temporary file for the concatenated chain
	tmpFile, err := ioutil.TempFile("", "scep-proxy-tls-chain-*.pem")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()

	// Write server cert + CA cert (concatenated)
	if _, err := tmpFile.Write(serverCertPEM); err != nil {
		tmpFile.Close()
		return "", nil, fmt.Errorf("failed to write server cert to temp file: %v", err)
	}

	// Add newline if server cert doesn't end with one
	if len(serverCertPEM) > 0 && serverCertPEM[len(serverCertPEM)-1] != '\n' {
		tmpFile.Write([]byte("\n"))
	}

	if _, err := tmpFile.Write(caCertPEM); err != nil {
		tmpFile.Close()
		return "", nil, fmt.Errorf("failed to write CA cert to temp file: %v", err)
	}

	if err := tmpFile.Close(); err != nil {
		return "", nil, fmt.Errorf("failed to close temp file: %v", err)
	}

	cleanup = func() {
		if err := os.Remove(tmpPath); err != nil {
			log.Printf("Warning: failed to remove temp cert file %s: %v", tmpPath, err)
		}
	}

	log.Printf("Created TLS certificate chain file: %s (server cert + CA cert)", tmpPath)
	return tmpPath, cleanup, nil
}

func (p *scepProxy) handleSCEPProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, maxPayloadSize))
	if err != nil {
		log.Printf("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Unwrap and verify AuthContainer
	scepReqBytes, err := p.unwrapFromAuthContainer(body)
	if err != nil {
		log.Printf("Failed to unwrap AuthContainer: %v", err)
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	// Parse SCEP proxy request
	scepReq := &proxy.SCEPProxyRequest{}
	if err := proto.Unmarshal(scepReqBytes, scepReq); err != nil {
		log.Printf("Failed to unmarshal SCEPProxyRequest: %v", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	log.Printf("Proxying SCEP request: %s", scepReq.String())

	// Forward to SCEP server
	scepResp, err := p.forwardToSCEPServer(scepReq)
	if err != nil {
		log.Printf("Failed to forward to SCEP server: %v", err)
		http.Error(w, "Proxy error", http.StatusBadGateway)
		return
	}

	// Marshal response
	scepRespBytes, err := proto.Marshal(scepResp)
	if err != nil {
		log.Printf("Failed to marshal SCEPProxyResponse: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Wrap in AuthContainer and sign
	authContainerBytes, err := p.wrapIntoAuthContainer(scepRespBytes)
	if err != nil {
		log.Printf("Failed to wrap response: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Send response
	w.Header().Set("Content-Type", "application/x-proto-binary")
	w.WriteHeader(http.StatusOK)
	w.Write(authContainerBytes)
}

func (p *scepProxy) forwardToSCEPServer(req *proxy.SCEPProxyRequest) (*proxy.SCEPProxyResponse, error) {
	// Determine HTTP method
	method := "GET"
	if req.HttpMethod == proxy.HTTPMethod_HTTP_METHOD_POST {
		method = "POST"
	}

	// Parse the SCEP server URL
	parsedURL, err := url.Parse(req.ScepServerUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SCEP server URL: %v", err)
	}

	// Add query parameters
	// The operation parameter is ALWAYS required in the query string, even for POST
	if req.Operation != proxy.SCEPOperation_SCEP_OPERATION_UNSPECIFIED {
		operation := operationToString(req.Operation)
		if operation != "" {
			query := parsedURL.Query()
			query.Set("operation", operation)

			// For GET requests, add message parameter (base64 URL-encoded)
			if method == "GET" && len(req.Message) > 0 {
				encoded := base64.URLEncoding.EncodeToString(req.Message)
				query.Set("message", encoded)
			}

			parsedURL.RawQuery = query.Encode()
		}
	}

	scepURL := parsedURL.String()

	// Create HTTP request
	var bodyReader io.Reader
	if method == "POST" {
		bodyReader = bytes.NewReader(req.Message)
	}

	httpReq, err := http.NewRequest(method, scepURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %v", err)
	}

	// Add headers from request
	for _, headerField := range req.GetHttpHeaderFields() {
		httpReq.Header.Set(headerField.GetName(), headerField.GetValue())
	}

	// Set content type for POST
	if method == "POST" {
		httpReq.Header.Set("Content-Type", "application/x-pki-message")
	}
	log.Printf("Forwarded HTTP request: %+v", httpReq)

	// Send request
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to SCEP server: %v", err)
	}
	defer httpResp.Body.Close()

	// Read response
	respBody, err := ioutil.ReadAll(io.LimitReader(httpResp.Body, maxPayloadSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read SCEP server response: %v", err)
	}

	// Build response header field list
	var respHeaderFields []*proxy.HTTPHeaderField
	for key, values := range httpResp.Header {
		for _, value := range values {
			respHeaderFields = append(respHeaderFields, &proxy.HTTPHeaderField{
				Name:  key,
				Value: value,
			})
		}
	}

	// Build SCEP proxy response
	scepResp := &proxy.SCEPProxyResponse{
		ScepServerUrl:    req.ScepServerUrl,
		Operation:        req.Operation,
		Message:          respBody,
		HttpStatusCode:   uint32(httpResp.StatusCode),
		HttpHeaderFields: respHeaderFields,
	}

	// Include error body if status >= 400
	if httpResp.StatusCode >= 400 {
		scepResp.ErrorBody = respBody
	}
	log.Printf("Forwarding SCEP response: %s", scepResp.String())

	return scepResp, nil
}

func (p *scepProxy) unwrapFromAuthContainer(authContainerBytes []byte) ([]byte, error) {
	authContainer := &auth.AuthContainer{}
	if err := proto.Unmarshal(authContainerBytes, authContainer); err != nil {
		return nil, fmt.Errorf("auth container unmarshal error: %v", err)
	}

	if err := p.verifyAuthContainer(authContainer); err != nil {
		return nil, err
	}

	return authContainer.ProtectedPayload.GetPayload(), nil
}

func (p *scepProxy) verifyAuthContainer(authContainer *auth.AuthContainer) error {
	if err := p.verifyAuthContainerHeader(authContainer); err != nil {
		return err
	}

	payload := authContainer.ProtectedPayload.GetPayload()
	if err := verifyAuthSig(authContainer.GetSignatureHash(), payload, p.clientCert); err != nil {
		return err
	}

	return nil
}

func (p *scepProxy) verifyAuthContainerHeader(authContainer *auth.AuthContainer) error {
	senderHash := authContainer.GetSenderCertHash()
	if len(senderHash) != hashSha256Len16 && len(senderHash) != hashSha256Len32 {
		return fmt.Errorf("unexpected senderCertHash length (%d)", len(senderHash))
	}

	switch authContainer.Algo {
	case evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_32BYTES:
		if !bytes.Equal(senderHash, p.clientCertHash) {
			return fmt.Errorf("client cert hash does not match (32 bytes)")
		}
	case evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_16BYTES:
		if !bytes.Equal(senderHash, p.clientCertHash[:hashSha256Len16]) {
			return fmt.Errorf("client cert hash does not match (16 bytes)")
		}
	default:
		return fmt.Errorf("unsupported hash algorithm")
	}

	return nil
}

func (p *scepProxy) wrapIntoAuthContainer(payload []byte) ([]byte, error) {
	body := auth.AuthBody{
		Payload: payload,
	}

	authContainer := auth.AuthContainer{}
	authContainer.ProtectedPayload = &body
	authContainer.SenderCertHash = p.proxyCertHash
	authContainer.Algo = evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_32BYTES

	sig, err := signAuthPayload(payload, p.proxyKey)
	if err != nil {
		return nil, fmt.Errorf("auth container signature error: %v", err)
	}
	authContainer.SignatureHash = sig

	wrappedPayload, err := proto.Marshal(&authContainer)
	if err != nil {
		return nil, fmt.Errorf("auth container marshal error: %v", err)
	}

	return wrappedPayload, nil
}

// Helper functions

func computeSha(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	return h.Sum(nil)
}

func signAuthPayload(payload []byte, key *ecdsa.PrivateKey) ([]byte, error) {
	payloadHash := computeSha(payload)
	r, s, err := ecdsa.Sign(rand.Reader, key, payloadHash)
	if err != nil {
		return nil, fmt.Errorf("ecdsa sign error: %v", err)
	}
	signature, err := rsCombinedBytes(r.Bytes(), s.Bytes(), &key.PublicKey)
	if err != nil {
		return nil, err
	}
	return signature, nil
}

func rsCombinedBytes(rBytes, sBytes []byte, pubKey *ecdsa.PublicKey) ([]byte, error) {
	keySize, err := ecdsaKeyBytes(pubKey)
	if err != nil {
		return nil, fmt.Errorf("ecdsa key bytes error: %v", err)
	}

	rsize := len(rBytes)
	ssize := len(sBytes)
	if rsize > keySize || ssize > keySize {
		return nil, fmt.Errorf("invalid sizes: keySize %d, rSize %d, sSize %d",
			keySize, rsize, ssize)
	}

	buffer := make([]byte, keySize*2)
	startPos := keySize - rsize
	copy(buffer[startPos:], rBytes)
	startPos = keySize*2 - ssize
	copy(buffer[startPos:], sBytes)
	return buffer, nil
}

func verifyAuthSig(signatureHash []byte, payload []byte, cert *x509.Certificate) error {
	payloadHash := computeSha(payload)

	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, payloadHash, signatureHash)

	case *ecdsa.PublicKey:
		sigHalflen, err := ecdsaKeyBytes(pub)
		if err != nil {
			return err
		}
		rbytes := signatureHash[0:sigHalflen]
		sbytes := signatureHash[sigHalflen:]
		r := new(big.Int).SetBytes(rbytes)
		s := new(big.Int).SetBytes(sbytes)

		if !ecdsa.Verify(pub, payloadHash, r, s) {
			return errors.New("ecdsa signature verification failed")
		}
		return nil

	default:
		return errors.New("unknown type of public key")
	}
}

func ecdsaKeyBytes(pubKey *ecdsa.PublicKey) (int, error) {
	curveBits := pubKey.Curve.Params().BitSize
	keyBytes := curveBits / 8
	if curveBits%8 > 0 {
		keyBytes++
	}
	if keyBytes%8 > 0 {
		return 0, fmt.Errorf("ecdsa pubkey size error, curveBits: %d", curveBits)
	}
	return keyBytes, nil
}

func loadPEMCertFromFile(path string) (*x509.Certificate, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	pemBlock, _ := pem.Decode(data)
	if pemBlock == nil {
		return nil, errors.New("PEM decode failed")
	}

	return x509.ParseCertificate(pemBlock.Bytes)
}

func loadECDSAKeyFromFile(path string) (*ecdsa.PrivateKey, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	pemBlock, _ := pem.Decode(data)
	if pemBlock == nil {
		return nil, errors.New("PEM decode failed")
	}

	switch pemBlock.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(pemBlock.Bytes)

	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(pemBlock.Bytes)
		if err != nil {
			return nil, err
		}
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS#8 key is not ECDSA")
		}
		return ecKey, nil

	default:
		return nil, errors.New("unsupported PEM type: " + pemBlock.Type)
	}
}

func operationToString(op proxy.SCEPOperation) string {
	switch op {
	case proxy.SCEPOperation_SCEP_OPERATION_GET_CA_CAPS:
		return "GetCACaps"
	case proxy.SCEPOperation_SCEP_OPERATION_GET_CA_CERT:
		return "GetCACert"
	case proxy.SCEPOperation_SCEP_OPERATION_GET_NEXT_CA_CERT:
		return "GetNextCACert"
	case proxy.SCEPOperation_SCEP_OPERATION_PKI_MESSAGE:
		return "PKIOperation"
	default:
		return ""
	}
}
