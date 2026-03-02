package scepclient

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	eveconfig "github.com/lf-edge/eve-api/go/config"
	eveinfo "github.com/lf-edge/eve-api/go/info"
	eveproxy "github.com/lf-edge/eve-api/go/proxy"
	"github.com/lf-edge/eve/pkg/pillar/cipher"
	"github.com/lf-edge/eve/pkg/pillar/controllerconn"
	"github.com/lf-edge/eve/pkg/pillar/netdump"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/smallstep/scep"
	"github.com/smallstep/scep/x509util"
	"google.golang.org/protobuf/proto"
)

func (c *SCEPClient) handleSCEPProfile(profile types.SCEPProfile, deleted bool) {
	var enrolledCert types.EnrolledCertificateStatus
	enrolledCertObj, err := c.pubEnrolledCertStatus.Get(profile.ProfileName)
	if err == nil && enrolledCertObj != nil {
		enrolledCert = enrolledCertObj.(types.EnrolledCertificateStatus)
	}

	if deleted {
		c.log.Noticef("Clearing EnrolledCertificateStatus for deleted SCEP profile %s",
			profile.ProfileName)
		if enrolledCert.CertFilepath != "" {
			if err = os.Remove(enrolledCert.CertFilepath); err != nil {
				c.log.Errorf(
					"Failed to remove certificate file %q for deleted SCEP profile %q: %v",
					enrolledCert.CertFilepath, profile.ProfileName, err)
			}
		}
		if enrolledCert.PrivateKeyFilepath != "" {
			if err = os.Remove(enrolledCert.PrivateKeyFilepath); err != nil {
				c.log.Errorf(
					"Failed to remove private key file %q for deleted SCEP profile %q: %v",
					enrolledCert.PrivateKeyFilepath, profile.ProfileName, err)
			}
		}
		if err = c.pubEnrolledCertStatus.Unpublish(profile.ProfileName); err != nil {
			c.log.Errorf("Failed to un-publish EnrolledCertificateStatus for profile %s",
				profile.ProfileName)
		}
		return
	}

	if enrolledCert.EnrollmentServerURL == profile.SCEPServerURL &&
		enrolledCert.CSRProfile.Equal(profile.CSRProfile) {
		// Certificate is already enrolled (or previously failed and will be
		// retried by the renewOrRetry ticker), and the enrollment profile
		// has not changed.
		return
	}

	c.log.Noticef("Processing created or modified SCEP profile %s",
		profile.ProfileName)

	// A new SCEP profile was added or an existing profile was modified by the user.
	// Reset the enrolled certificate state (in case it is not a zero value) to start
	// a fresh enrollment.
	enrolledCert.CertEnrollmentProfileName = profile.ProfileName
	enrolledCert.EnrollmentServerURL = profile.SCEPServerURL
	enrolledCert.CSRProfile = profile.CSRProfile
	enrolledCert.Error = types.ErrorDescription{}
	enrolledCert.Subject = types.CertDistinguishedName{}
	enrolledCert.Issuer = types.CertDistinguishedName{}
	enrolledCert.SAN = types.CertSubjectAlternativeName{}
	if profile.CSRProfile.RenewPeriodPercent != 0 {
		enrolledCert.RenewPeriodPercent = profile.CSRProfile.RenewPeriodPercent
	} else {
		enrolledCert.RenewPeriodPercent = defaultRenewPeriod
	}
	if profile.CSRProfile.KeyType != eveconfig.KeyType_KEY_TYPE_UNSPECIFIED {
		enrolledCert.KeyType = profile.CSRProfile.KeyType
	} else {
		enrolledCert.KeyType = defaultKeyType
	}
	hashAlgorithm := profile.CSRProfile.HashAlgorithm
	if hashAlgorithm != eveconfig.HashAlgorithm_HASH_ALGORITHM_UNSPECIFIED {
		enrolledCert.HashAlgorithm = hashAlgorithm
	} else {
		enrolledCert.HashAlgorithm = defaultHashAlgorithm
	}
	enrolledCert.IssueTimestamp = time.Time{}
	enrolledCert.ExpirationTimestamp = time.Time{}
	enrolledCert.SHA256Fingerprint = ""
	enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_UNSPECIFIED

	if enrolledCert.CertFilepath != "" {
		if err = os.Remove(enrolledCert.CertFilepath); err != nil {
			c.log.Errorf("Failed to remove obsolete certificate file %q "+
				"for modified SCEP profile %q: %v", enrolledCert.CertFilepath,
				profile.ProfileName, err)
		}
		enrolledCert.CertFilepath = ""
	}

	if enrolledCert.PrivateKeyFilepath != "" {
		if err = os.Remove(enrolledCert.PrivateKeyFilepath); err != nil {
			c.log.Errorf("Failed to remove obsolete private key file %q "+
				"for modified SCEP profile %q: %v", enrolledCert.PrivateKeyFilepath,
				profile.ProfileName, err)
		}
		enrolledCert.PrivateKeyFilepath = ""
	}

	if profile.ParsingError.Error != "" {
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_INVALID_CONFIG
		enrolledCert.Error = profile.ParsingError
		c.publishEnrolledCertStatus(enrolledCert)
		return
	}

	privateKey, err := c.makePrivateKey(enrolledCert.KeyType)
	if err != nil {
		err = fmt.Errorf("failed to generate private key: %v", err)
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_ENROLLMENT_FAILED
		enrolledCert.Error.SetErrorDescription(types.ErrorDescription{Error: err.Error()})
		c.publishEnrolledCertStatus(enrolledCert)
		return
	}

	enrolledCert.PrivateKeyFilepath, err = c.savePrivateKey(profile.ProfileName, privateKey)
	if err != nil {
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_ENROLLMENT_FAILED
		enrolledCert.Error.SetErrorDescription(types.ErrorDescription{Error: err.Error()})
		c.publishEnrolledCertStatus(enrolledCert)
		return
	}

	cert, pending, err := c.enrollOrRenewCertificate(profile, nil, privateKey)
	if err != nil {
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_ENROLLMENT_FAILED
		enrolledCert.Error.SetErrorDescription(types.ErrorDescription{Error: err.Error()})
		c.publishEnrolledCertStatus(enrolledCert)
		return
	}

	if pending {
		// No need to persist the SCEP transaction ID.
		// The transaction ID is deterministically derived from the certificate
		// public key (via SubjectKeyIdentifier). As long as the key is preserved,
		// the same transaction ID will be regenerated on retry.
		// Therefore, storing it separately is unnecessary.
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_PENDING
		c.publishEnrolledCertStatus(enrolledCert)
		return
	}

	enrolledCert.CertFilepath, err = c.saveCertificate(profile.ProfileName, cert)
	if err != nil {
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_ENROLLMENT_FAILED
		enrolledCert.Error.SetErrorDescription(types.ErrorDescription{Error: err.Error()})
		c.publishEnrolledCertStatus(enrolledCert)
		return
	}

	c.populateCertStatus(&enrolledCert, cert)
	enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_AVAILABLE
	c.publishEnrolledCertStatus(enrolledCert)
}

// makePrivateKey generates a new private key for SCEP enrollment.
// Only RSA keys are currently supported. While the SCEP RFC allows
// non-encryption-capable keys (e.g., DSA or ECDSA) to be used with
// the CMS PasswordRecipientInfo mechanism (RFC3211) for PKCS#7 encryption,
// there is no open-source SCEP client that supports it, and many SCEP
// servers do not implement it either.
// As a result, ECDSA keys cannot be used for SCEP enrollment at this time.
func (c *SCEPClient) makePrivateKey(keyType eveconfig.KeyType) (SignerAndDecrypter, error) {
	switch keyType {
	case eveconfig.KeyType_KEY_TYPE_RSA_2048:
		return rsa.GenerateKey(rand.Reader, 2048)

	case eveconfig.KeyType_KEY_TYPE_RSA_3072:
		return rsa.GenerateKey(rand.Reader, 3072)

	case eveconfig.KeyType_KEY_TYPE_RSA_4096:
		return rsa.GenerateKey(rand.Reader, 4096)

	case eveconfig.KeyType_KEY_TYPE_ECDSA_P256,
		eveconfig.KeyType_KEY_TYPE_ECDSA_P384,
		eveconfig.KeyType_KEY_TYPE_ECDSA_P521:
		return nil, fmt.Errorf("ECDSA keys are not supported for SCEP enrollment " +
			"due to lack of PasswordRecipientInfo support")

	case eveconfig.KeyType_KEY_TYPE_UNSPECIFIED:
		return nil, errors.New("unspecified key type")

	default:
		return nil, fmt.Errorf("unsupported key type: %v", keyType)
	}
}

// enrollOrRenewCertificate performs SCEP certificate enrollment or renewal.
//
// If currentCert is nil, this is an initial enrollment: a CSR is generated,
// signed with a short-lived self-signed bootstrap certificate, and sent to the
// SCEP server via the controller proxy.
//
// If currentCert is not nil, this is a renewal: a CSR is generated and signed
// using the current certificate and its private key.
//
// The function returns the issued certificate on success, or a boolean
// indicating whether the request is pending (SCEP PENDING status).
// Errors are returned if the operation fails or the SCEP response is invalid.
func (c *SCEPClient) enrollOrRenewCertificate(profile types.SCEPProfile,
	currentCert *x509.Certificate,
	privateKey SignerAndDecrypter) (cert *x509.Certificate, pending bool, err error) {
	if currentCert == nil {
		c.log.Noticef("Enrolling a new certificate for profile %s", profile.ProfileName)
	} else {
		c.log.Noticef("Renewing certificate for profile %s", profile.ProfileName)
	}

	// Decrypt challenge password (if configured).
	challengePassword, err := c.decryptChallengePassword(
		profile.EncryptedChallengePassword)
	if err != nil {
		return nil, false, err
	}

	// Create CSR for the requested profile.
	csr, err := c.makeCSR(profile.CSRProfile, privateKey, challengePassword)
	if err != nil {
		return nil, false, err
	}

	signerCert := currentCert
	if signerCert == nil {
		// No current certificate exists; create a short-lived self-signed certificate
		// to sign the initial PKCSReq for SCEP enrollment (bootstrap only).
		signerCert, err = c.makeSelfSignedCert(privateKey, csr)
		if err != nil {
			return nil, false, err
		}
	}

	// Parse configured CA certificates used to validate SCEP responses.
	// Errors are ignored here because configuration was validated earlier.
	var caCerts []*x509.Certificate
	for _, certBytes := range profile.CACertPEM {
		block, _ := pem.Decode(certBytes)
		if block != nil {
			if caCert, _ := x509.ParseCertificate(block.Bytes); caCert != nil {
				caCerts = append(caCerts, caCert)
			}
		}
	}

	// Build PKCSReq message template.
	var msgType scep.MessageType
	if currentCert != nil {
		msgType = scep.RenewalReq
	} else {
		msgType = scep.PKCSReq
	}
	pkiTemplate := &scep.PKIMessage{
		MessageType: msgType,
		Recipients:  caCerts,
		SignerKey:   privateKey,
		SignerCert:  signerCert,
	}
	if challengePassword != "" {
		pkiTemplate.CSRReqMessage = &scep.CSRReqMessage{
			ChallengePassword: challengePassword,
		}
	}

	reqMsg, err := scep.NewCSRRequest(csr, pkiTemplate)
	if err != nil {
		err = fmt.Errorf("failed to create SCEP PKCSReq message: %w", err)
		return nil, false, err
	}

	var respBytes []byte
	if profile.UseControllerProxy {
		respBytes, err = c.execPKIOperationOverProxy(profile, reqMsg)
	} else {
		respBytes, err = c.execPKIOperationDirectly(profile, reqMsg)
	}
	if err != nil {
		return nil, false, err
	}

	// Parse and validate SCEP PKI message.
	respMsg, err := scep.ParsePKIMessage(respBytes, scep.WithCACerts(caCerts))
	if err != nil {
		err = fmt.Errorf("failed to parse and validate SCEP PKI message: %w", err)
		return nil, false, err
	}

	// Handle SCEP PKI status.
	switch respMsg.PKIStatus {
	case scep.FAILURE:
		err = fmt.Errorf("SCEP server responded with FAILURE status: %s",
			respMsg.FailInfo)
		return nil, false, err

	case scep.PENDING:
		return nil, true, nil
	}

	// Decrypt issued certificate envelope.
	if err = respMsg.DecryptPKIEnvelope(signerCert, privateKey); err != nil {
		err = fmt.Errorf("failed to decrypt SCEP certificate response envelope: %w", err)
		return nil, false, err
	}

	if respMsg.CertRepMessage == nil || respMsg.CertRepMessage.Certificate == nil {
		err = fmt.Errorf("SCEP response did not contain an issued certificate")
		return nil, false, err
	}

	return respMsg.CertRepMessage.Certificate, false, nil
}

func (c *SCEPClient) execPKIOperationOverProxy(profile types.SCEPProfile,
	req *scep.PKIMessage) (respBytes []byte, err error) {
	// Prepare proxy request to controller.
	proxyReq := &eveproxy.SCEPProxyRequest{
		ScepProfileName:  profile.ProfileName,
		Operation:        eveproxy.SCEPOperation_SCEP_OPERATION_PKI_MESSAGE,
		Message:          req.Raw,
		HttpMethod:       eveproxy.HTTPMethod_HTTP_METHOD_POST,
		HttpHeaderFields: []*eveproxy.HTTPHeaderField{}, // no extra headers required
	}

	proxyReqBytes, err := proto.Marshal(proxyReq)
	if err != nil {
		err = fmt.Errorf("failed to marshal SCEPProxyRequest: %w", err)
		return nil, err
	}

	proxyURL := controllerconn.URLPathString(
		c.controllerHostname,
		c.httpClient.UsingV2API(),
		c.devUUID,
		"proxy/scep",
	)

	ctx, cancel := c.httpClient.GetContextForAllIntfFunctions()
	retval, err := c.httpClient.SendOnAllIntf(
		ctx,
		proxyURL,
		bytes.NewBuffer(proxyReqBytes),
		controllerconn.RequestOptions{
			WithNetTracing: true,
			NetTraceFolder: types.NetTraceFolder,
			BailOnHTTPErr:  true,
			Iteration:      c.iteration,
		},
	)
	cancel()
	c.iteration++
	if err != nil {
		c.publishSCEPNetdump(retval.TracedReqs, false)
		err = fmt.Errorf("SCEP proxy request failed (HTTP status %d): %w",
			retval.Status, err)
		return nil, err
	}

	// Verify controller authentication container.
	if err = c.httpClient.RemoveAndVerifyAuthContainer(&retval, false); err != nil {
		c.publishSCEPNetdump(retval.TracedReqs, false)
		err = fmt.Errorf("failed to verify SCEP proxy response authentication: %w", err)
		return nil, err
	}

	proxyResp := &eveproxy.SCEPProxyResponse{}
	if err = proto.Unmarshal(retval.RespContents, proxyResp); err != nil {
		c.publishSCEPNetdump(retval.TracedReqs, false)
		err = fmt.Errorf("failed to unmarshal SCEPProxyResponse: %w", err)
		return nil, err
	}

	// Check proxied HTTP status returned by SCEP server.
	if proxyResp.HttpStatusCode >= 400 {
		c.publishSCEPNetdump(retval.TracedReqs, false)
		const maxBody = 1024
		body := proxyResp.ErrorBody
		if len(body) > maxBody {
			body = body[:maxBody]
		}
		err = fmt.Errorf("SCEP server returned HTTP %d: %s",
			proxyResp.HttpStatusCode, strings.TrimSpace(string(body)))
		return nil, err
	}

	c.publishSCEPNetdump(retval.TracedReqs, true)
	return proxyResp.Message, nil
}

func (c *SCEPClient) execPKIOperationDirectly(profile types.SCEPProfile,
	req *scep.PKIMessage) (respBytes []byte, err error) {
	scepServerURL, err := url.Parse(profile.SCEPServerURL)
	if err != nil {
		// This should be unreachable. Zedagent validates SCEP server URL and SCEPClient
		// ignores SCEP profiles with invalid config.
		err = fmt.Errorf("failed to parse SCEP server URL: %w", err)
		return nil, err
	}

	query := scepServerURL.Query()
	query.Set("operation", "PKIOperation")
	scepServerURL.RawQuery = query.Encode()

	header := make(http.Header)
	header.Set("Content-Type", "application/x-pki-message")

	ctx, cancel := c.httpClient.GetContextForAllIntfFunctions()
	retval, err := c.httpClient.SendOnAllIntf(
		ctx,
		scepServerURL.String(),
		bytes.NewBuffer(req.Raw),
		controllerconn.RequestOptions{
			WithNetTracing: true,
			NetTraceFolder: types.NetTraceFolder,
			BailOnHTTPErr:  true,
			CustomHeader:   header,
			Iteration:      c.iteration,
		},
	)
	cancel()
	c.iteration++
	if err != nil {
		c.publishSCEPNetdump(retval.TracedReqs, false)
		err = fmt.Errorf("SCEP request failed (HTTP status %d): %w",
			retval.Status, err)
		return nil, err
	}

	c.publishSCEPNetdump(retval.TracedReqs, true)
	return retval.RespContents, nil
}

func (c *SCEPClient) decryptChallengePassword(
	encryptedPassword types.CipherBlockStatus) (string, error) {
	if !encryptedPassword.IsCipher {
		if c.cipherMetrics != nil {
			c.cipherMetrics.RecordFailure(c.log, types.NoData)
		}
		return "", nil
	}
	status, decBlock, err := cipher.GetCipherCredentials(
		&cipher.DecryptCipherContext{
			Log:                  c.log,
			AgentName:            agentName,
			AgentMetrics:         c.cipherMetrics,
			PubSubControllerCert: c.subControllerCert,
			PubSubEdgeNodeCert:   c.subEdgeNodeCert,
		},
		encryptedPassword)
	if err != nil {
		err = fmt.Errorf("failed to decrypt SCEP challenge password: %w", err)
		return "", err
	}
	if status.HasError() {
		err = fmt.Errorf("failed to decrypt SCEP challenge password: %s", status.Error)
		return "", err
	}
	return decBlock.SCEPChallengePassword, nil
}

func (c *SCEPClient) makeCSR(profile types.CSRProfile, privateKey SignerAndDecrypter,
	challengePassword string) (*x509.CertificateRequest, error) {
	template := &x509util.CertificateRequest{
		CertificateRequest: x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName:         profile.Subject.CommonName,
				Organization:       profile.Subject.Organization,
				OrganizationalUnit: profile.Subject.OrganizationalUnit,
				Country:            profile.Subject.Country,
				Province:           profile.Subject.State,
				Locality:           profile.Subject.Locality,
			},
			DNSNames:       profile.SAN.DNSNames,
			EmailAddresses: profile.SAN.EmailAddresses,
			IPAddresses:    profile.SAN.IPAddresses,
		},
		ChallengePassword: challengePassword,
	}
	for _, uriStr := range profile.SAN.URIs {
		// url.Parse implements a generic RFC 3986 URI parser (despite the name).
		// The error is intentionally ignored here because the configuration
		// has already been validated by zedagent, and any invalid CSR profile
		// is skipped earlier by SCEPClient.
		uri, _ := url.Parse(uriStr)
		template.URIs = append(template.URIs, uri)
	}

	// Select Signature Algorithm
	hashAlg := profile.HashAlgorithm
	if hashAlg == eveconfig.HashAlgorithm_HASH_ALGORITHM_UNSPECIFIED {
		hashAlg = defaultHashAlgorithm
	}
	keyType := profile.KeyType
	if keyType == eveconfig.KeyType_KEY_TYPE_UNSPECIFIED {
		keyType = defaultKeyType
	}
	sigAlg, err := selectSignatureAlgorithm(keyType, hashAlg)
	if err != nil {
		return nil, err
	}
	template.SignatureAlgorithm = sigAlg

	// Create CSR
	csrDER, err := x509util.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse generated CSR: %w", err)
	}
	return csr, nil
}

// makeSelfSignedCert creates a short-lived self-signed certificate used
// exclusively for the initial SCEP enrollment (not for renewal).
// At this stage the client does not yet possess a CA-issued certificate,
// so this temporary certificate is used solely to sign the PKCS#7
// enrollment request and prove possession of the private key.
// It is not used for authentication, trust chaining, or any purpose beyond
// bootstrapping enrollment.
func (c *SCEPClient) makeSelfSignedCert(
	priv crypto.Signer, csr *x509.CertificateRequest) (*x509.Certificate, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %s", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(time.Hour * 1)
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "SCEP SIGNER",
			Organization: csr.Subject.Organization,
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(
		rand.Reader, &template, &template, priv.Public(), priv)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create self-signed SCEP bootstrap certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to parse generated self-signed SCEP bootstrap certificate: %w", err)
	}

	return cert, nil
}

// Run periodically to:
//   - Retry certificates that previously failed to enroll or renew.
//   - Re-run enrollment/renewal for certificates for which the SCEP server
//     returned PENDING (e.g. awaiting administrative approval).
//   - Check all enrolled certificates and initiate renewal attempts
//     for those that have entered their renewal window.
func (c *SCEPClient) retryAndStartRenew() {
	now := time.Now()

	for _, profileObj := range c.subSCEPProfile.GetAll() {
		profile := profileObj.(types.SCEPProfile)

		var enrolledCert types.EnrolledCertificateStatus
		enrolledCertObj, err := c.pubEnrolledCertStatus.Get(profile.ProfileName)
		if err == nil && enrolledCertObj != nil {
			enrolledCert = enrolledCertObj.(types.EnrolledCertificateStatus)
		}

		// Determine the next action.
		var enrollNewCert, renewCert bool

		switch enrolledCert.CertStatus {
		case eveinfo.CertStatus_CERT_STATUS_UNSPECIFIED:
			enrollNewCert = true

		case eveinfo.CertStatus_CERT_STATUS_AVAILABLE:
			// Determine if certificate is inside the renewal window.
			validity := enrolledCert.ExpirationTimestamp.Sub(enrolledCert.IssueTimestamp)
			if validity > 0 {
				renewPercent := enrolledCert.RenewPeriodPercent
				renewTime := enrolledCert.IssueTimestamp.Add(
					time.Duration(int64(validity) * int64(renewPercent) / 100),
				)
				if now.After(renewTime) {
					renewCert = true
				}
			}

		case eveinfo.CertStatus_CERT_STATUS_PENDING:
			// If no cert exists yet → enrollment pending.
			// If cert exists → renewal pending.
			if enrolledCert.CertFilepath == "" {
				enrollNewCert = true
			} else {
				renewCert = true
			}

		case eveinfo.CertStatus_CERT_STATUS_EXPIRED:
			renewCert = true

		case eveinfo.CertStatus_CERT_STATUS_ENROLLMENT_FAILED:
			enrollNewCert = true

		case eveinfo.CertStatus_CERT_STATUS_RENEWAL_FAILED:
			renewCert = true

		case eveinfo.CertStatus_CERT_STATUS_INVALID_CONFIG:
			// Do nothing with this SCEP profile.
		}

		if !renewCert && !enrollNewCert {
			continue
		}

		// Set failure and pending states to use if the enrollment or renewal
		// operation executed below fails or is still in progress.
		var failureState, pendingState eveinfo.CertStatus
		if enrollNewCert {
			failureState = eveinfo.CertStatus_CERT_STATUS_ENROLLMENT_FAILED
			pendingState = eveinfo.CertStatus_CERT_STATUS_PENDING
		} else if renewCert {
			if enrolledCert.ExpirationTimestamp.Before(now) {
				failureState = eveinfo.CertStatus_CERT_STATUS_EXPIRED
				pendingState = eveinfo.CertStatus_CERT_STATUS_EXPIRED
			} else {
				failureState = eveinfo.CertStatus_CERT_STATUS_RENEWAL_FAILED
				pendingState = eveinfo.CertStatus_CERT_STATUS_RENEWAL_PENDING
			}
		}

		// Load or generate private key
		var privateKey SignerAndDecrypter

		if enrolledCert.PrivateKeyFilepath != "" {
			privateKey, err = c.loadPrivateKey(enrolledCert.PrivateKeyFilepath)
			if err != nil {
				enrolledCert.CertStatus = failureState
				errStr := fmt.Sprintf("failed to load private key: %v", err)
				enrolledCert.Error.SetErrorDescription(
					types.ErrorDescription{Error: errStr},
				)
				c.publishEnrolledCertStatus(enrolledCert)
				continue
			}
		} else {
			privateKey, err = c.makePrivateKey(enrolledCert.KeyType)
			if err != nil {
				enrolledCert.CertStatus = failureState
				errStr := fmt.Sprintf("failed to generate private key: %v", err)
				enrolledCert.Error.SetErrorDescription(
					types.ErrorDescription{Error: errStr},
				)
				c.publishEnrolledCertStatus(enrolledCert)
				continue
			}

			enrolledCert.PrivateKeyFilepath, err = c.savePrivateKey(
				profile.ProfileName, privateKey)
			if err != nil {
				enrolledCert.CertStatus = failureState
				errStr := fmt.Sprintf("failed to save private key: %v", err)
				enrolledCert.Error.SetErrorDescription(
					types.ErrorDescription{Error: errStr},
				)
				c.publishEnrolledCertStatus(enrolledCert)
				continue
			}
		}

		// Enrollment or renewal
		var cert *x509.Certificate
		var pending bool

		if enrollNewCert {
			cert, pending, err = c.enrollOrRenewCertificate(
				profile, nil, privateKey)
		} else {
			// Load currently enrolled certificate for signing the SCEP renewal request.
			currentCert, err := c.loadCertificate(enrolledCert.CertFilepath)
			if err != nil {
				enrolledCert.CertStatus = failureState
				errMsg := fmt.Sprintf("failed to load current certificate: %v", err)
				enrolledCert.Error.SetErrorDescription(
					types.ErrorDescription{Error: errMsg},
				)
				c.publishEnrolledCertStatus(enrolledCert)
				continue
			}
			cert, pending, err = c.enrollOrRenewCertificate(
				profile, currentCert, privateKey)
		}

		if err != nil {
			enrolledCert.CertStatus = failureState
			enrolledCert.Error.SetErrorDescription(
				types.ErrorDescription{Error: err.Error()},
			)
			c.publishEnrolledCertStatus(enrolledCert)
			continue
		}

		if pending {
			enrolledCert.CertStatus = pendingState
			c.publishEnrolledCertStatus(enrolledCert)
			continue
		}

		// Save new certificate
		enrolledCert.CertFilepath, err = c.saveCertificate(profile.ProfileName, cert)
		if err != nil {
			enrolledCert.CertStatus = failureState
			errMsg := fmt.Sprintf("failed to save certificate: %v", err)
			enrolledCert.Error.SetErrorDescription(
				types.ErrorDescription{Error: errMsg},
			)
			c.publishEnrolledCertStatus(enrolledCert)
			continue
		}

		c.populateCertStatus(&enrolledCert, cert)
		enrolledCert.CertStatus = eveinfo.CertStatus_CERT_STATUS_AVAILABLE
		enrolledCert.Error = types.ErrorDescription{}
		c.publishEnrolledCertStatus(enrolledCert)
	}
}

// populateCertStatus copies certificate-derived fields from cert
// into the provided EnrolledCertificateStatus.
// It does not modify renewal settings, status, key type, hash algorithm or file paths.
func (c *SCEPClient) populateCertStatus(status *types.EnrolledCertificateStatus,
	cert *x509.Certificate) {
	if status == nil || cert == nil {
		return
	}

	status.Subject = pkixNameToCertDistinguishedName(cert.Subject)
	status.Issuer = pkixNameToCertDistinguishedName(cert.Issuer)

	status.SAN = types.CertSubjectAlternativeName{
		DNSNames:       cert.DNSNames,
		EmailAddresses: cert.EmailAddresses,
		IPAddresses:    cert.IPAddresses,
	}
	for _, uri := range cert.URIs {
		status.SAN.URIs = append(status.SAN.URIs, uri.String())
	}

	status.IssueTimestamp = cert.NotBefore
	status.ExpirationTimestamp = cert.NotAfter

	sum := sha256.Sum256(cert.Raw)
	status.SHA256Fingerprint = hex.EncodeToString(sum[:])
}

func (c *SCEPClient) publishEnrolledCertStatus(status types.EnrolledCertificateStatus) {
	oldStatus, err := c.pubEnrolledCertStatus.Get(status.Key())
	if err == nil && oldStatus != nil {
		c.log.Functionf("Publishing EnrolledCertStatus(%s) update: %s",
			status.Key(), cmp.Diff(oldStatus, status))
	} else {
		c.log.Functionf("Publishing new EnrolledCertStatus(%s): %+v",
			status.Key(), status)
	}
	err = c.pubEnrolledCertStatus.Publish(status.Key(), status)
	if err != nil {
		c.log.Errorf("Failed to publish EnrolledCertificateStatus for profile %s",
			status.CertEnrollmentProfileName)
	}
}

// Publish netdump containing traces of executed SCEP requests.
func (c *SCEPClient) publishSCEPNetdump(
	tracedReqs []netdump.TracedNetRequest, success bool) {
	netDumper := c.netDumper
	if netDumper == nil {
		return
	}
	topic := netDumpConfigOKTopic
	if !success {
		topic = netDumpConfigFailTopic
	}
	filename, err := netDumper.Publish(topic, types.NetTraceFolder, tracedReqs...)
	if err != nil {
		c.log.Warnf("Failed to publish netdump for topic %s: %v", topic, err)
	} else {
		c.log.Noticef("Published netdump for topic %s: %s", topic, filename)
	}
}

func (c *SCEPClient) savePrivateKey(profileName string,
	key SignerAndDecrypter) (path string, err error) {
	if key == nil {
		return "", errors.New("nil private key")
	}
	if err = os.MkdirAll(privateKeyDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create private key directory: %w", err)
	}

	derBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("failed to marshal private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: derBytes,
	})

	path = c.getPrivateKeyFilePath(profileName)
	if err = os.WriteFile(path, pemBytes, 0600); err != nil {
		return "", fmt.Errorf("failed to write private key: %w", err)
	}
	return path, nil
}

func (c *SCEPClient) loadPrivateKey(path string) (SignerAndDecrypter, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file %q: %w", path, err)
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("invalid PEM block in private key file %q", path)
	}

	// Parse PKCS#8 (this is how savePrivateKey writes it)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PKCS#8 private key in %q: %w", path, err)
	}

	// Make sure it is RSA (only supported)
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		err = fmt.Errorf(
			"unsupported private key type in %q: only RSA is supported", path)
		return nil, err
	}
	return rsaKey, nil
}

func (c *SCEPClient) saveCertificate(profileName string,
	cert *x509.Certificate) (path string, err error) {
	if cert == nil {
		return "", errors.New("nil certificate")
	}
	if err = os.MkdirAll(certDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cert directory: %w", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})

	path = c.getCertFilePath(profileName)
	if err = os.WriteFile(path, pemBytes, 0644); err != nil {
		return "", fmt.Errorf("failed to write certificate: %w", err)
	}
	return path, nil
}

func (c *SCEPClient) loadCertificate(path string) (*x509.Certificate, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file %q: %w", path, err)
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid PEM block in certificate file %q", path)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate in %q: %w", path, err)
	}
	return cert, nil
}

func (c *SCEPClient) getCertFilePath(profileName string) string {
	return filepath.Join(certDir, profileName+"-cert.pem")
}

func (c *SCEPClient) getPrivateKeyFilePath(profileName string) string {
	return filepath.Join(privateKeyDir, profileName+"-key.pem")
}

func pkixNameToCertDistinguishedName(name pkix.Name) types.CertDistinguishedName {
	return types.CertDistinguishedName{
		CommonName:         name.CommonName,
		SerialNumber:       name.SerialNumber,
		Organization:       name.Organization,
		OrganizationalUnit: name.OrganizationalUnit,
		Country:            name.Country,
		State:              name.Province,
		Locality:           name.Locality,
	}
}

func selectSignatureAlgorithm(keyType eveconfig.KeyType,
	hashAlg eveconfig.HashAlgorithm) (x509.SignatureAlgorithm, error) {

	switch keyType {
	case eveconfig.KeyType_KEY_TYPE_RSA_2048,
		eveconfig.KeyType_KEY_TYPE_RSA_3072,
		eveconfig.KeyType_KEY_TYPE_RSA_4096:

		switch hashAlg {
		case eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA256:
			return x509.SHA256WithRSA, nil
		case eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA384:
			return x509.SHA384WithRSA, nil
		case eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA512:
			return x509.SHA512WithRSA, nil
		default:
			return 0, fmt.Errorf("unsupported hash algorithm for RSA: %v", hashAlg)
		}

	case eveconfig.KeyType_KEY_TYPE_ECDSA_P256,
		eveconfig.KeyType_KEY_TYPE_ECDSA_P384,
		eveconfig.KeyType_KEY_TYPE_ECDSA_P521:

		switch hashAlg {
		case eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA256:
			return x509.ECDSAWithSHA256, nil
		case eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA384:
			return x509.ECDSAWithSHA384, nil
		case eveconfig.HashAlgorithm_HASH_ALGORITHM_SHA512:
			return x509.ECDSAWithSHA512, nil
		default:
			return 0, fmt.Errorf("unsupported hash algorithm for ECDSA: %v", hashAlg)
		}

	default:
		return 0, fmt.Errorf("unsupported key type: %v", keyType)
	}
}
