package utils

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"fmt"

	"github.com/lf-edge/eve-api/go/auth"
	"github.com/lf-edge/eve-api/go/evecommon"
)

func PrepareAuthContainer(payload []byte, signingCert *x509.Certificate,
	signingKey *ecdsa.PrivateKey) (*auth.AuthContainer, error) {
	certHash := sha256.Sum256(CertToPEM(signingCert))
	payloadHash := sha256.Sum256(payload)
	signatureOfPayloadHash, err := computeEcdsaSignature(payloadHash[:], signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to compute ECDSA signature: %w", err)
	}
	authContainer := &auth.AuthContainer{}
	authContainer.ProtectedPayload = &auth.AuthBody{Payload: payload}
	authContainer.Algo = evecommon.HashAlgorithm_HASH_ALGORITHM_SHA256_32BYTES
	authContainer.SenderCertHash = certHash[:]
	authContainer.SignatureHash = signatureOfPayloadHash
	return authContainer, nil
}
