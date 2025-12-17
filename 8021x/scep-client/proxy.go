package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	"net/url"

	"github.com/go-kit/kit/log"
	httptransport "github.com/go-kit/kit/transport/http"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"

	scepclient "github.com/micromdm/scep/v2/client"
	scepserver "github.com/micromdm/scep/v2/server"

	"github.com/lf-edge/eve-api/go/auth"
	"github.com/lf-edge/eve-api/go/evecommon"
	"github.com/lf-edge/eve-api/go/proxy"
)

type scepProxyArgs struct {
	logger       log.Logger
	proxyURLStr  string
	proxyURL     *url.URL
	serverURLStr string
	serverURL    *url.URL
	clientCert   *x509.Certificate
	clientKey    *ecdsa.PrivateKey
	serverCert   *x509.Certificate
	tlsCaCert    *x509.Certificate
}

func (args *scepProxyArgs) parseURLs() error {
	var err error
	args.serverURL, err = url.Parse(args.serverURLStr)
	if err != nil {
		return err
	}
	// No scheme → default to HTTP for SCEP server
	if args.serverURL.Scheme == "" {
		args.serverURL.Scheme = "http"
	}
	// Enforce HTTP for SCEP server
	if args.serverURL.Scheme != "http" {
		return fmt.Errorf("unsupported scheme for SCEP server: %s", args.serverURL.Scheme)
	}

	args.proxyURL, err = url.Parse(args.proxyURLStr)
	if err != nil {
		return err
	}
	// No scheme → default to HTTPS for SCEP proxy
	if args.proxyURL.Scheme == "" {
		args.proxyURL.Scheme = "https"
	}
	// Enforce HTTPS for SCEP proxy
	if args.proxyURL.Scheme != "https" {
		return fmt.Errorf("unsupported scheme for SCEP proxy: %s", args.proxyURL.Scheme)
	}
	return nil
}

func newScepProxy(args scepProxyArgs) (scepclient.Client, error) {
	if err := args.parseURLs(); err != nil {
		return nil, err
	}
	endpoints, err := makeProxyEndpoints(args)
	if err != nil {
		return nil, err
	}
	logMiddleware := scepserver.EndpointLoggingMiddleware(args.logger)
	endpoints.GetEndpoint = logMiddleware(endpoints.GetEndpoint)
	endpoints.PostEndpoint = logMiddleware(endpoints.PostEndpoint)
	return endpoints, nil
}

func makeProxyEndpoints(args scepProxyArgs) (*scepserver.Endpoints, error) {
	// Create HTTP client that trusts the given CA cert.
	var rootCAs *x509.CertPool
	if args.tlsCaCert != nil {
		rootCAs = x509.NewCertPool()
		rootCAs.AddCert(args.tlsCaCert)
	}
	tlsConfig := &tls.Config{
		RootCAs: rootCAs,
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	httpClient := &http.Client{
		Transport: transport,
	}

	codec := proxyScepCodec{
		scepProxyArgs: args,
	}
	options := []httptransport.ClientOption{
		httptransport.SetClient(httpClient),
	}

	return &scepserver.Endpoints{
		GetEndpoint: httptransport.NewClient(
			"GET",
			args.serverURL,
			codec.encodeRequest,
			codec.decodeResponse,
			options...).Endpoint(),
		PostEndpoint: httptransport.NewClient(
			"POST",
			args.serverURL,
			codec.encodeRequest,
			codec.decodeResponse,
			options...).Endpoint(),
	}, nil
}

type proxyScepCodec struct {
	scepProxyArgs
	clientCertHash []byte
	serverCertHash []byte
}

func (c proxyScepCodec) encodeRequest(
	ctx context.Context, r *http.Request, request interface{}) error {
	req := request.(scepserver.SCEPRequest)

	// from github.com/micromdm/scep/v2/server/endpoint.go
	const (
		getCACaps     = "GetCACaps"
		getCACert     = "GetCACert"
		getNextCACert = "GetNextCACert"
		pkiOperation  = "PKIOperation"
	)
	var operation proxy.SCEPOperation
	switch req.Operation {
	case getCACaps:
		operation = proxy.SCEPOperation_SCEP_OPERATION_GET_CA_CAPS
	case getCACert:
		operation = proxy.SCEPOperation_SCEP_OPERATION_GET_CA_CERT
	case getNextCACert:
		operation = proxy.SCEPOperation_SCEP_OPERATION_GET_NEXT_CA_CERT
	case pkiOperation:
		operation = proxy.SCEPOperation_SCEP_OPERATION_PKI_MESSAGE
	default:
		return fmt.Errorf("unexpected SCEP operation: %s", req.Operation)
	}

	var httpMethod proxy.HTTPMethod
	switch r.Method {
	case "GET":
		httpMethod = proxy.HTTPMethod_HTTP_METHOD_GET
	case "POST":
		httpMethod = proxy.HTTPMethod_HTTP_METHOD_POST
	default:
		return fmt.Errorf("scep: HTTP %s method not supported", r.Method)
	}

	var httpHeaderFields []*proxy.HTTPHeaderField
	for key, values := range r.Header {
		for _, value := range values {
			httpHeaderFields = append(httpHeaderFields,
				&proxy.HTTPHeaderField{
					Name:  key,
					Value: value,
				})
		}
	}

	proxyReq := &proxy.SCEPProxyRequest{
		ScepServerUrl:    r.URL.String(),
		Operation:        operation,
		Message:          req.Message,
		HttpMethod:       httpMethod,
		HttpHeaderFields: httpHeaderFields,
		// TODO: - maybe add CA identifier string for GetCACert
		//       - but this is optional and likely not used much
	}
	proxyReqBytes, err := proto.Marshal(proxyReq)
	if err != nil {
		return fmt.Errorf("failed to marshal SCEPProxyRequest: %v", err)
	}

	authContainerBytes, err := c.wrapIntoAuthContainer(proxyReqBytes)
	if err != nil {
		return err
	}
	body := bytes.NewReader(authContainerBytes)

	// Redirect the request towards proxy.
	r2, err := http.NewRequest("POST", c.proxyURL.String(), body)
	if err != nil {
		return errors.Wrapf(err, "creating new POST request for %s", req.Operation)
	}
	r2.Header.Set("Content-Type", "application/x-proto-binary")
	*r = *r2
	c.logger.Log("HTTP Request", fmt.Sprintf("%+v", r),
		"Proxy Request", proxyReq.String())
	return nil
}

const (
	maxPayloadSize  = 2 << 20
	certChainHeader = "application/x-x509-ca-ra-cert"
)

func (c proxyScepCodec) decodeResponse(
	ctx context.Context, r *http.Response) (interface{}, error) {
	c.logger.Log("HTTP Response", fmt.Sprintf("%+v", r))
	if r.StatusCode != http.StatusOK && r.StatusCode >= 400 {
		body, _ := ioutil.ReadAll(io.LimitReader(r.Body, maxPayloadSize))
		return nil, fmt.Errorf("http request failed with status %s, msg: %s",
			r.Status,
			string(body),
		)
	}
	data, err := ioutil.ReadAll(io.LimitReader(r.Body, maxPayloadSize))
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	payload, err := c.unwrapFromAuthContainer(data)
	if err != nil {
		return nil, err
	}

	proxyResp := &proxy.SCEPProxyResponse{}
	err = proto.Unmarshal(payload, proxyResp)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal SCEPProxyResponse: %v", err)
	}
	c.logger.Log("Proxy Response", proxyResp.String())

	resp := scepserver.SCEPResponse{
		Data: proxyResp.GetMessage(),
	}
	header := r.Header.Get("Content-Type")
	if header == certChainHeader {
		// we only set it to two to indicate a cert chain.
		// the actual number of certs will be in the payload.
		resp.CACertNum = 2
	}
	return resp, nil
}

func (c proxyScepCodec) wrapIntoAuthContainer(payload []byte) ([]byte, error) {
	if len(c.clientCertHash) == 0 {
		c.clientCertHash = computeSha(c.clientCert.Raw)
	}

	body := auth.AuthBody{
		Payload: payload,
	}
	sm := auth.AuthContainer{}
	sm.ProtectedPayload = &body
	sm.SenderCertHash = c.clientCertHash
	sm.Algo = evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_32BYTES

	sig, err := signAuthPayload(payload, c.clientKey)
	if err != nil {
		return nil, fmt.Errorf("auth container signature error: %v", err)
	}
	sm.SignatureHash = sig

	wrappedPayload, err := proto.Marshal(&sm)
	if err != nil {
		return nil, fmt.Errorf("auth container marshal error: %v", err)
	}
	return wrappedPayload, nil
}

// Check proxy signature and unwrap the SCEP response from the AuthContainer.
func (c proxyScepCodec) unwrapFromAuthContainer(authContainerBytes []byte) ([]byte, error) {
	if len(c.serverCertHash) == 0 {
		c.serverCertHash = computeSha(c.serverCert.Raw)
	}

	authContainer := &auth.AuthContainer{}
	err := proto.Unmarshal(authContainerBytes, authContainer)
	if err != nil {
		return nil, fmt.Errorf("auth container unmarshal error: %v", err)
	}

	err = c.verifyAuthContainer(authContainer)
	if err != nil {
		return nil, err
	}
	return authContainer.ProtectedPayload.GetPayload(), nil
}

func (c proxyScepCodec) verifyAuthContainer(authContainer *auth.AuthContainer) error {
	err := c.verifyAuthContainerHeader(authContainer)
	if err != nil {
		return err
	}
	// Verify payload integrity
	payload := authContainer.ProtectedPayload.GetPayload()
	err = verifyAuthSig(authContainer.GetSignatureHash(), payload, c.serverCert)
	if err != nil {
		return err
	}
	return nil
}

const (
	hashSha256Len16 = 16 // senderCertHash size of 16
	hashSha256Len32 = 32 // size of 32 bytes
)

func (c proxyScepCodec) verifyAuthContainerHeader(authContainer *auth.AuthContainer) error {
	if len(authContainer.GetSenderCertHash()) != hashSha256Len16 &&
		len(authContainer.GetSenderCertHash()) != hashSha256Len32 {
		err := fmt.Errorf("unexpected senderCertHash length (%d)",
			len(authContainer.GetSenderCertHash()))
		return err
	}

	switch authContainer.Algo {
	case evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_32BYTES:
		if bytes.Compare(authContainer.GetSenderCertHash(), c.serverCertHash) != 0 {
			err := fmt.Errorf("local server cert hash does not match in authen (32 bytes)")
			return err
		}
	case evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_16BYTES:
		if bytes.Compare(authContainer.GetSenderCertHash(), c.serverCertHash[:hashSha256Len16]) != 0 {
			err := fmt.Errorf("local server cert hash does not match in authen (16 bytes)")
			return err
		}
	default:
		err := fmt.Errorf("VerifyAuthContainerHeader: hash algorithm is not supported")
		return err
	}
	return nil
}

func computeSha(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	hash := h.Sum(nil)
	return hash
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

	// Basically the size is 32 bytes.
	// The r and s needs to be both left padded to two 32 bytes slice
	// into a single signature buffer
	buffer := make([]byte, keySize*2)
	startPos := keySize - rsize
	copy(buffer[startPos:], rBytes)
	startPos = keySize*2 - ssize
	copy(buffer[startPos:], sBytes)
	return buffer[:], nil
}

// verify the signed data with controller certificate public key
func verifyAuthSig(signatureHash []byte, payload []byte, cert *x509.Certificate) error {
	payloadHash := computeSha(payload)
	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, payloadHash, signatureHash)
		if err != nil {
			return err
		}
	case *ecdsa.PublicKey:
		sigHalflen, err := ecdsaKeyBytes(pub)
		if err != nil {
			return err
		}
		rbytes := signatureHash[0:sigHalflen]
		sbytes := signatureHash[sigHalflen:]
		r := new(big.Int)
		s := new(big.Int)
		r.SetBytes(rbytes)
		s.SetBytes(sbytes)
		ok := ecdsa.Verify(pub, payloadHash, r, s)
		if !ok {
			return errors.New("ecdsa image signature verification failed")
		}
	default:
		return errors.New("unknown type of public key")
	}
	return nil
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
