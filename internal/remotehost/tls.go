package remotehost

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

/**
 * [INPUT]: 依赖 crypto/* 与 os 标准库
 * [OUTPUT]: 对外提供 LoadOrCreateCert（自签 ECDSA P-256 证书，私钥 0600）与 Fingerprint（证书 DER 的 SHA-256 十六进制）
 * [POS]: Remote Host 的传输信任根：手机按指纹固定，不依赖 CA；证书只在缺失/过期/损坏时重建
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	certFile     = "host-cert.pem"
	keyFile      = "host-key.pem"
	certLifetime = 10 * 365 * 24 * time.Hour
	// renewBefore rebuilds a certificate that is about to expire; a rebuilt
	// certificate changes the fingerprint, so phones must pair again.
	renewBefore = 30 * 24 * time.Hour
)

// Fingerprint returns the lower-case hex SHA-256 of a certificate's DER bytes.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// LoadOrCreateCert returns the Host's TLS certificate from dir, creating a
// self-signed one on first use. The private key file is mode 0600.
func LoadOrCreateCert(dir string, now time.Time) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	if cert, fp, ok := loadCert(certPath, keyPath, now); ok {
		return cert, fp, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create remote host dir: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Solco Remote Host"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writeFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	cert, fp, ok := loadCert(certPath, keyPath, now)
	if !ok {
		return tls.Certificate{}, "", errors.New("remote host certificate unreadable after creation")
	}
	return cert, fp, nil
}

func loadCert(certPath, keyPath string, now time.Time) (tls.Certificate, string, bool) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil || len(cert.Certificate) == 0 {
		return tls.Certificate{}, "", false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || now.After(leaf.NotAfter.Add(-renewBefore)) || now.Before(leaf.NotBefore) {
		return tls.Certificate{}, "", false
	}
	return cert, Fingerprint(cert.Certificate[0]), true
}

// writeFileAtomic writes data to a temp file in the same directory, sets mode
// and renames it over path, so readers never see a partial file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
