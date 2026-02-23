package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/google/go-tpm/legacy/tpm2"
	"github.com/google/go-tpm/tpmutil"
)

// TpmPrivateKey implements crypto.Signer and crypto.Decrypter
type TpmPrivateKey struct {
	handle tpmutil.Handle
	pub    crypto.PublicKey
	rwc    io.ReadWriteCloser
}

func (t *TpmPrivateKey) Public() crypto.PublicKey {
	return t.pub
}

func (t *TpmPrivateKey) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	fmt.Println("TpmPrivateKey.Sign called")
	var hashAlg tpm2.Algorithm

	// 1. Determine the hash algorithm from opts
	switch opts.HashFunc() {
	case crypto.SHA256:
		hashAlg = tpm2.AlgSHA256
	case crypto.SHA1:
		hashAlg = tpm2.AlgSHA1
	case crypto.SHA384:
		hashAlg = tpm2.AlgSHA384
	case crypto.SHA512:
		hashAlg = tpm2.AlgSHA512
	default:
		return nil, fmt.Errorf("unsupported hash function: %v", opts.HashFunc())
	}

	// 2. Determine the padding/scheme
	// Most 802.1x/EAP-TLS uses PKCS#1v15 (RSASSA).
	// If you need PSS support, you'd check if opts is *rsa.PSSOptions.
	scheme := &tpm2.SigScheme{
		Alg:  tpm2.AlgRSASSA,
		Hash: hashAlg,
	}

	// 3. Perform the Sign operation
	sig, err := tpm2.Sign(t.rwc, t.handle, "", digest, nil, scheme)
	if err != nil {
		return nil, fmt.Errorf("TPM Sign failed: %w", err)
	}

	return sig.RSA.Signature, nil
}

func (t *TpmPrivateKey) Decrypt(rand io.Reader, msg []byte, opts crypto.DecrypterOpts) ([]byte, error) {
	fmt.Println("TpmPrivateKey.Decrypt called")
	return tpm2.RSADecrypt(t.rwc, t.handle, "", msg, &tpm2.AsymScheme{Alg: tpm2.AlgRSAES}, "")
}

// To check the key presence, use:
//
//   - tpm2_getcap handles-persistent
//   - tpm2_readpublic -c 0x81000005
//
// To remove the created key, use:
//
//   - tpm2_evictcontrol -C o -c 0x81000005
//
// To check the TPM2 provider for openssl:
//
// export TPM2OPENSSL_TCTI="device:/dev/tpm0"
// openssl list -providers
// openssl rsa -provider tpm2  -in "handle:0x81000005" -pubout
func loadOrMakeKey(path string, rsaBits int) (crypto.Signer, error) {
	const tpmHandle = tpmutil.Handle(0x81000005)
	const tpmPath = "/dev/tpmrm0"

	// 1. Try to initialize TPM
	rwc, err := tpm2.OpenTPM(tpmPath)
	if err == nil {
		// Check if key already exists at the persistent handle
		pub, _, _, err := tpm2.ReadPublic(rwc, tpmHandle)
		if err == nil {
			fmt.Printf("Using existing TPM key at 0x%x\n", tpmHandle)
			rsaPub, _ := pub.Key()
			return &TpmPrivateKey{handle: tpmHandle, pub: rsaPub, rwc: rwc}, nil
		}

		// Key not there, let's make it
		pubKey, err := createTpmKey(rwc, tpmHandle, rsaBits)
		if err == nil {
			return &TpmPrivateKey{handle: tpmHandle, pub: pubKey, rwc: rwc}, nil
		}
		fmt.Printf("Failed to create key using TPM: %v\n", err)
		rwc.Close() // Close if TPM failed so we can use files
	}

	// 2. Fallback to File logic
	fmt.Println("TPM unavailable or failed, falling back to filesystem")
	return loadOrMakeFileKey(path, rsaBits)
}

func createTpmKey(rwc io.ReadWriteCloser,
	persistentHandle tpmutil.Handle, bits int) (crypto.PublicKey, error) {
	template := tpm2.Public{
		Type:    tpm2.AlgRSA,
		NameAlg: tpm2.AlgSHA256,
		// Attributes: Keep your current ones, but ensure FlagUserWithAuth is there
		Attributes: tpm2.FlagNoDA | tpm2.FlagSensitiveDataOrigin | tpm2.FlagUserWithAuth |
			tpm2.FlagSign | tpm2.FlagDecrypt,
		RSAParameters: &tpm2.RSAParams{
			KeyBits: uint16(bits),
		},
	}

	/* For ECC:
	template = tpm2.Public{
		Type:    tpm2.AlgECC,
		NameAlg: tpm2.AlgSHA256,
		Attributes: tpm2.FlagSign | tpm2.FlagNoDA | tpm2.FlagDecrypt |
			tpm2.FlagSensitiveDataOrigin |
			tpm2.FlagUserWithAuth,
		ECCParameters: &tpm2.ECCParams{
			CurveID: tpm2.CurveNISTP256,
		},
	}
	*/

	// 1. Create the Primary Key in the Owner Hierarchy
	// This generates the key in a transient slot first.
	signerHandle, pubKey, err := tpm2.CreatePrimary(rwc, tpm2.HandleOwner,
		tpm2.PCRSelection{}, "", "", template)
	if err != nil {
		return nil, fmt.Errorf("CreatePrimary failed: %w", err)
	}
	// Ensure we flush the transient handle when we're done (if eviction fails)
	defer tpm2.FlushContext(rwc, signerHandle)

	// 2. Attempt to evict whatever is currently at the persistent handle
	// We ignore the error here because if the slot is already empty, it will return an error,
	// which is fine—we just want to make sure the slot is clear.
	_ = tpm2.EvictControl(rwc, "", tpm2.HandleOwner, persistentHandle, persistentHandle)

	// 3. Persist the transient signerHandle to the persistentHandle
	err = tpm2.EvictControl(rwc, "", tpm2.HandleOwner, signerHandle, persistentHandle)
	if err != nil {
		return nil, fmt.Errorf("failed to persist key to 0x%x: %w", persistentHandle, err)
	}

	return pubKey, nil
}

func loadOrMakeFileKey(path string, rsaBits int) (crypto.Signer, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return loadKeyFromFile(path)
		}
		return nil, err
	}
	defer file.Close()

	// Generate standard RSA key
	priv, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return nil, err
	}

	// Encode to PEM
	privBytes := x509.MarshalPKCS1PrivateKey(priv)
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privBytes,
	}
	if err := pem.Encode(file, pemBlock); err != nil {
		return nil, err
	}

	return priv, nil
}

func loadKeyFromFile(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
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
		// SEC 1, RFC 5915
		return x509.ParseECPrivateKey(pemBlock.Bytes)

	case "PRIVATE KEY":
		// PKCS#8
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
