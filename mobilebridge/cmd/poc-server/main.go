// poc-server is the G0 proof-of-concept peer: a Tailcat server with a self-signed HTTPS echo behind it, printing the
// address, certificate fingerprint and the (single) client public key it admits. Development only, not shipped.
//
//	go run ./cmd/poc-server -client <publicKey>      # admit exactly that phone node key
//
// Endpoints: POST /echo (echoes body, reflects Authorization), GET /ws (websocket echo, subprotocol solco.bearer).
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/coder/websocket"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

func main() {
	clientPub := flag.String("client", "", "admitted phone node public key text (empty admits all: PoC only)")
	gen := flag.Bool("gen-client", false, "generate a phone node key, print it, and admit only its public key")
	flag.Parse()
	if *gen {
		priv := key.NewNode()
		privText, _ := priv.MarshalText()
		pubText, _ := priv.Public().MarshalText()
		fmt.Printf("KEY=%s\n", privText)
		*clientPub = string(pubText)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	srv := &tailcat.Server{Logf: func(string, ...any) {}}
	srv.AllowClient = func(k key.NodePublic) bool {
		text, _ := k.MarshalText()
		return *clientPub == "" || string(text) == *clientPub
	}
	ln, err := srv.Listen(ctx, "tcp", ":7788")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer srv.Close()

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "poc"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	sum := sha256.Sum256(der)

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen-Auth", r.Header.Get("Authorization"))
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"solco.bearer"}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil || c.Write(r.Context(), typ, data) != nil {
				return
			}
		}
	})
	hs := &http.Server{Handler: mux, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}}}}
	fmt.Printf("ADDR=%s\nFP=%s\n", srv.TailcatAddr(), hex.EncodeToString(sum[:]))
	go hs.ServeTLS(ln, "", "")
	<-ctx.Done()
	hs.Close()
}
