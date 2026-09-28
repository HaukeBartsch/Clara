package mailer

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"csms/api/internal/config"
)

// relayCert is the self-signed localhost certificate served by the fake relay
// over implicit TLS and after STARTTLS. TestMain installs it in the process
// trust store, so Send verifies it the same way it verifies a real relay.
var relayCert tls.Certificate

// TestMain puts a self-signed certificate for localhost into the system trust
// store before the first TLS dial happens: crypto/x509 reads SSL_CERT_FILE only
// once per process, and mailer.Send has no injection point of its own (the
// standard library's default verifier is used deliberately).
func TestMain(m *testing.M) {
	cert, certPEM, err := newRelayCert()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mailer test: generate relay certificate: ", err)
		os.Exit(1)
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("clara-mailer-test-ca-%d.pem", os.Getpid()))
	if err = os.WriteFile(path, certPEM, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "mailer test: write trust file: ", err)
		os.Exit(1)
	}
	os.Setenv("SSL_CERT_FILE", path)
	relayCert = cert

	code := m.Run()
	os.Remove(path)
	os.Exit(code)
}

// newRelayCert builds a self-signed certificate valid for localhost, returning
// it as a server certificate together with the PEM to trust it.
func newRelayCert() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cp, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return cp, certPEM, nil
}

// relayCmd is one SMTP command as the relay received it; secure marks the ones
// that arrived on an encrypted channel.
type relayCmd struct {
	line   string
	secure bool
}

// relay is a deliberately small SMTP server — just enough of RFC 5321 for the
// conversation Send performs, and nothing more. It records the exchange so the
// tests can assert on what the client actually sent, not only on the error.
type relay struct {
	implicitTLS    bool // speak TLS from the first byte (submission over 465)
	startTLS       bool // advertise STARTTLS and accept it
	advertiseAuth  bool // advertise AUTH PLAIN, checked against user/password
	authOnlySecure bool // advertise AUTH only once the channel is encrypted
	user           string
	password       string

	rejectMailFrom bool
	rejectRcpt     bool

	mu       sync.Mutex
	cmds     []relayCmd
	from     string // MAIL FROM argument
	rcpt     []string
	data     string
	authUser string
	authPass string
}

// serve starts the relay on a loopback port and returns the port number.
func (r *relay) serve(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed together with the test
			}
			go r.handle(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func (r *relay) tlsConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{relayCert}}
}

// handle runs one SMTP conversation. It never calls into t, which would be a
// race from this goroutine; failures show up as protocol errors the client
// reports instead.
func (r *relay) handle(conn net.Conn) {
	defer conn.Close()
	// Bound every read so a broken test fails rather than hangs.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	var secure bool
	if r.implicitTLS {
		tc := tls.Server(conn, r.tlsConfig())
		if err := tc.Handshake(); err != nil {
			return
		}
		conn, secure = tc, true
	}
	fmt.Fprint(conn, "220 fake-relay ESMTP\r\n")

	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		r.mu.Lock()
		r.cmds = append(r.cmds, relayCmd{line: cmd, secure: secure})
		r.mu.Unlock()

		word := cmd
		if i := strings.IndexByte(word, ' '); i >= 0 {
			word = word[:i]
		}
		switch strings.ToUpper(word) {
		case "EHLO":
			fmt.Fprint(conn, "250-fake-relay\r\n250-SIZE 1048576\r\n")
			if r.startTLS && !secure {
				fmt.Fprint(conn, "250-STARTTLS\r\n")
			}
			if r.advertiseAuth && (!r.authOnlySecure || secure) {
				fmt.Fprint(conn, "250-AUTH PLAIN\r\n")
			}
			fmt.Fprint(conn, "250 OK\r\n")
		case "HELO":
			fmt.Fprint(conn, "250 fake-relay\r\n")
		case "STARTTLS":
			if !r.startTLS || secure {
				fmt.Fprint(conn, "454 4.7.13 TLS not available\r\n")
				continue
			}
			fmt.Fprint(conn, "220 Ready to start TLS\r\n")
			tc := tls.Server(conn, r.tlsConfig())
			if err = tc.Handshake(); err != nil {
				return
			}
			// The client waits for the handshake before sending anything else,
			// so nothing was buffered unread on the old reader.
			conn, secure = tc, true
			br = bufio.NewReader(conn)
		case "AUTH":
			user, pass, ok := parseAuthPlain(cmd)
			if !ok {
				fmt.Fprint(conn, "501 5.5.4 malformed AUTH argument\r\n")
				continue
			}
			r.mu.Lock()
			r.authUser, r.authPass = user, pass
			r.mu.Unlock()
			if user == r.user && pass == r.password {
				fmt.Fprint(conn, "235 2.7.0 Authentication successful\r\n")
			} else {
				fmt.Fprint(conn, "535 5.7.8 Authentication credentials invalid\r\n")
			}
		case "MAIL":
			if r.rejectMailFrom {
				fmt.Fprint(conn, "550 5.7.1 sender refused\r\n")
				continue
			}
			r.mu.Lock()
			r.from = envelopeAddress(cmd)
			r.mu.Unlock()
			fmt.Fprint(conn, "250 2.1.0 OK\r\n")
		case "RCPT":
			if r.rejectRcpt {
				fmt.Fprint(conn, "550 5.7.1 recipient refused\r\n")
				continue
			}
			r.mu.Lock()
			r.rcpt = append(r.rcpt, envelopeAddress(cmd))
			r.mu.Unlock()
			fmt.Fprint(conn, "250 2.1.5 OK\r\n")
		case "DATA":
			fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
			msg, err := readMessage(br)
			if err != nil {
				return
			}
			// Recorded before the reply: once a client sees 250 it may return,
			// and the test can then rely on the message being here.
			r.mu.Lock()
			r.data = msg
			r.mu.Unlock()
			fmt.Fprint(conn, "250 2.0.0 OK\r\n")
		case "QUIT":
			fmt.Fprint(conn, "221 2.0.0 Bye\r\n")
			return
		default:
			fmt.Fprint(conn, "500 5.5.2 Command unrecognized\r\n")
		}
	}
}

// readMessage consumes one DATA payload up to the terminating dot and undoes
// dot-stuffing (RFC 5321 §4.5.2).
func readMessage(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		if line == ".\r\n" || line == ".\n" {
			return b.String(), nil
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		b.WriteString(line)
	}
}

// envelopeAddress picks the address out of a "MAIL FROM:<addr>" or
// "RCPT TO:<addr>" command.
func envelopeAddress(cmd string) string {
	_, rest, _ := strings.Cut(cmd, ":")
	addr, _, _ := strings.Cut(rest, ">")
	return strings.Trim(strings.TrimSpace(addr), "<>")
}

// parseAuthPlain splits "AUTH PLAIN <base64>" into the identity and secret of
// the AUTHENTICATE string authzid NUL authcid NUL passwd.
func parseAuthPlain(cmd string) (user, pass string, ok bool) {
	parts := strings.Fields(cmd)
	if len(parts) != 3 || !strings.EqualFold(parts[1], "PLAIN") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", false
	}
	fields := strings.Split(string(raw), "\x00")
	if len(fields) != 3 {
		return "", "", false
	}
	return fields[1], fields[2], true
}

// snapshot copies what the relay recorded, for assertion without the lock.
func (r *relay) snapshot() (cmds []relayCmd, from string, rcpt []string, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]relayCmd(nil), r.cmds...), r.from,
		append([]string(nil), r.rcpt...), r.data
}

// cmdNames lists the recorded commands in order, upper-cased and without
// arguments — enough to assert on protocol sequence.
func cmdNames(cmds []relayCmd) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		name := c.line
		if i := strings.IndexByte(name, ' '); i >= 0 {
			name = name[:i]
		}
		names = append(names, strings.ToUpper(name))
	}
	return names
}

// configFor builds a configuration pointing at a relay on the loopback port.
// The TLS tests pass "localhost", which is what both the test certificate and
// the client's ServerName name.
func configFor(host string, port int, security string) *config.Config {
	return &config.Config{
		SMTPHost:     host,
		SMTPPort:     port,
		SMTPSecurity: security,
		OTPMailFrom:  "no-reply@csms.example.org",
	}
}

// splitMessage separates the recorded message into headers and body. It also
// checks the line endings, which RFC 5322 requires to be CRLF.
func splitMessage(t *testing.T, msg string) (map[string]string, string) {
	t.Helper()
	if !strings.Contains(msg, "\r\n") {
		t.Errorf("message has no CRLF line endings:\n%q", msg)
	}
	head, body, found := strings.Cut(msg, "\r\n\r\n")
	if !found {
		t.Fatalf("message has no header/body separator:\n%q", msg)
	}
	headers := map[string]string{}
	for _, line := range strings.Split(head, "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Errorf("malformed header line %q", line)
			continue
		}
		key := strings.ToLower(name)
		if _, dup := headers[key]; dup {
			t.Errorf("header %q appears twice", key)
		}
		headers[key] = strings.TrimSpace(value)
	}
	return headers, body
}

func TestNewMapsConfiguration(t *testing.T) {
	cfg := &config.Config{
		SMTPHost: "relay.example.org", SMTPPort: 465, SMTPSecurity: "tls",
		SMTPUsername: "clara", SMTPPassword: "s3cret", OTPMailFrom: "no-reply@example.org",
	}
	m := New(cfg)
	if m.host != cfg.SMTPHost || m.port != cfg.SMTPPort || m.security != cfg.SMTPSecurity {
		t.Errorf("relay = %s:%d/%s, want %s:%d/%s",
			m.host, m.port, m.security, cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPSecurity)
	}
	if m.username != cfg.SMTPUsername || m.password != cfg.SMTPPassword {
		t.Errorf("credentials = %q/***", m.username)
	}
	if m.from != cfg.OTPMailFrom {
		t.Errorf("from = %q, want %q", m.from, cfg.OTPMailFrom)
	}
	if !m.Enabled() {
		t.Error("Enabled() = false for a configured relay")
	}
}

func TestEnabledWithoutRelay(t *testing.T) {
	var unset *Mailer
	if unset.Enabled() {
		t.Error("Enabled() = true for a nil Mailer")
	}
	if (&Mailer{}).Enabled() {
		t.Error("Enabled() = true without SMTP_HOST")
	}
	// Send must fail with a clear error instead of panicking (REQ-AUTH-057).
	err := unset.Send("user@example.org", "subject", "body")
	if err == nil || !strings.Contains(err.Error(), "no SMTP relay") {
		t.Errorf("Send() on an unusable Mailer = %v, want a 'no SMTP relay' error", err)
	}
}

func TestSendOverPlainRelay(t *testing.T) {
	r := &relay{}
	port := r.serve(t)

	m := New(configFor("127.0.0.1", port, "none"))
	if err := m.Send("user@example.org", "Your CLARA login code", "code 493021"); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	cmds, from, rcpt, data := r.snapshot()
	if got, want := strings.Join(cmdNames(cmds), " "), "EHLO MAIL RCPT DATA QUIT"; got != want {
		t.Errorf("conversation = %s, want %s", got, want)
	}
	for _, c := range cmds {
		if c.secure {
			t.Errorf("command %q arrived over TLS on a security=none relay", c.line)
		}
	}
	if from != "no-reply@csms.example.org" {
		t.Errorf("envelope sender = %q, want the configured OTP_MAIL_FROM", from)
	}
	if len(rcpt) != 1 || rcpt[0] != "user@example.org" {
		t.Errorf("envelope recipients = %v, want [user@example.org]", rcpt)
	}

	headers, body := splitMessage(t, data)
	for name, want := range map[string]string{
		"from":         "no-reply@csms.example.org",
		"to":           "user@example.org",
		"subject":      "Your CLARA login code",
		"mime-version": "1.0",
		"content-type": "text/plain; charset=utf-8",
	} {
		if headers[name] != want {
			t.Errorf("header %s = %q, want %q", name, headers[name], want)
		}
	}
	// A body that does not end in a newline gains one from closing the DATA
	// payload; the multi-line case below shows the verbatim round-trip.
	if got := strings.TrimSuffix(body, "\r\n"); got != "code 493021" {
		t.Errorf("body = %q, want the message body unchanged", got)
	}
	// The Date header must be a timestamp mail clients can parse.
	if _, err := time.Parse(time.RFC1123Z, headers["date"]); err != nil {
		t.Errorf("date header %q does not parse as RFC 1123Z: %v", headers["date"], err)
	}
}

func TestSendDeliversMultiLineBody(t *testing.T) {
	r := &relay{}
	port := r.serve(t)

	// The login-code text admin.sendLoginCode composes (REQ-AUTH-057).
	body := "Your CLARA login code is 493021.\r\n\r\n" +
		"It expires in 10 minutes and can be used once. If you did not try to\r\n" +
		"sign in, change your password and contact an administrator.\r\n"
	m := New(configFor("127.0.0.1", port, "none"))
	if err := m.Send("user@example.org", "Your CLARA login code", body); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	_, _, _, data := r.snapshot()
	_, got := splitMessage(t, data)
	if got != body {
		t.Errorf("body =\n%q\nwant\n%q", got, body)
	}
}

func TestSendAuthenticatesWhenConfigured(t *testing.T) {
	r := &relay{advertiseAuth: true, user: "clara-relay", password: "relay-pass"}
	port := r.serve(t)

	cfg := configFor("127.0.0.1", port, "none")
	m := New(cfg)
	m.username, m.password = "clara-relay", "relay-pass"
	if err := m.Send("user@example.org", "subject", "body"); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	cmds, _, _, _ := r.snapshot()
	if got, want := strings.Join(cmdNames(cmds), " "), "EHLO AUTH MAIL RCPT DATA QUIT"; got != want {
		t.Errorf("conversation = %s, want %s", got, want)
	}
	r.mu.Lock()
	authUser, authPass := r.authUser, r.authPass
	r.mu.Unlock()
	if authUser != "clara-relay" || authPass != "relay-pass" {
		t.Errorf("AUTH PLAIN carried (%q, %q), want (clara-relay, relay-pass)", authUser, authPass)
	}
}

func TestSendFailsWhenRelayHasNoAuth(t *testing.T) {
	r := &relay{} // anonymous relay: AUTH is never advertised
	port := r.serve(t)

	m := New(configFor("127.0.0.1", port, "none"))
	m.username, m.password = "clara-relay", "relay-pass"
	err := m.Send("user@example.org", "subject", "body")
	if err == nil || !strings.Contains(err.Error(), "does not advertise AUTH") {
		t.Fatalf("Send() = %v, want the 'does not advertise AUTH' error", err)
	}
	// The misconfiguration must be caught before any message is accepted.
	if _, from, _, _ := r.snapshot(); from != "" {
		t.Errorf("MAIL FROM %q was sent to a relay that cannot authenticate us", from)
	}
}

func TestSendReportsAuthFailureWithoutLeakingPassword(t *testing.T) {
	r := &relay{advertiseAuth: true, user: "clara-relay", password: "not-this-one"}
	port := r.serve(t)

	m := New(configFor("127.0.0.1", port, "none"))
	m.username, m.password = "clara-relay", "relay-pass"
	err := m.Send("user@example.org", "subject", "body")
	if err == nil || !strings.Contains(err.Error(), "auth as clara-relay") {
		t.Fatalf("Send() = %v, want an auth failure naming the user", err)
	}
	if strings.Contains(err.Error(), "relay-pass") {
		t.Errorf("error leaks the SMTP password: %v", err)
	}
	if _, from, _, _ := r.snapshot(); from != "" {
		t.Error("message was accepted although authentication failed")
	}
}

func TestSendUpgradesWithStartTLS(t *testing.T) {
	r := &relay{startTLS: true, advertiseAuth: true, authOnlySecure: true,
		user: "clara-relay", password: "relay-pass"}
	port := r.serve(t)

	cfg := configFor("localhost", port, "starttls")
	m := New(cfg)
	m.username, m.password = "clara-relay", "relay-pass"
	if err := m.Send("user@example.org", "subject", "body"); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	cmds, from, rcpt, data := r.snapshot()
	names := strings.Join(cmdNames(cmds), " ")
	if want := "EHLO STARTTLS EHLO AUTH MAIL RCPT DATA QUIT"; names != want {
		t.Errorf("conversation = %s, want %s", names, want)
	}
	// Credentials and message content must not have crossed the wire in clear.
	for _, c := range cmds {
		switch strings.ToUpper(strings.SplitN(c.line, " ", 2)[0]) {
		case "AUTH", "MAIL", "RCPT", "DATA":
			if !c.secure {
				t.Errorf("command %q was sent before the channel was encrypted", c.line)
			}
		}
	}
	if from != "no-reply@csms.example.org" || len(rcpt) != 1 || data == "" {
		t.Errorf("delivery = sender %q, recipients %v, %d bytes of message", from, rcpt, len(data))
	}
}

func TestSendOverImplicitTLS(t *testing.T) {
	r := &relay{implicitTLS: true}
	port := r.serve(t)

	m := New(configFor("localhost", port, "tls"))
	if err := m.Send("user@example.org", "subject", "body"); err != nil {
		t.Fatalf("Send(): %v", err)
	}

	cmds, _, _, data := r.snapshot()
	if got, want := strings.Join(cmdNames(cmds), " "), "EHLO MAIL RCPT DATA QUIT"; got != want {
		t.Errorf("conversation = %s, want %s", got, want)
	}
	for _, c := range cmds {
		if !c.secure {
			t.Errorf("command %q arrived in clear text on a TLS-only relay", c.line)
		}
	}
	if data == "" {
		t.Error("no message reached the relay")
	}
}

func TestSendStartTLSRequiredButUnavailable(t *testing.T) {
	r := &relay{} // no STARTTLS offered
	port := r.serve(t)

	err := New(configFor("127.0.0.1", port, "starttls")).Send("user@example.org", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "starttls") {
		t.Fatalf("Send() = %v, want a starttls error", err)
	}
	if _, from, _, _ := r.snapshot(); from != "" {
		t.Errorf("message was accepted without TLS although STARTTLS is configured: MAIL FROM %q", from)
	}
}

func TestSendImplicitTLSToPlainRelay(t *testing.T) {
	r := &relay{} // plaintext relay on a port configured as implicit TLS
	port := r.serve(t)

	err := New(configFor("localhost", port, "tls")).Send("user@example.org", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "dial") {
		t.Fatalf("Send() = %v, want a dial error from the failed TLS handshake", err)
	}
}

func TestSendReportsRelayRejections(t *testing.T) {
	for _, c := range []struct {
		name           string
		rejectMailFrom bool
		rejectRcpt     bool
		want           string
		step           string
	}{
		{"sender refused", true, false, "mail from", "MAIL FROM"},
		{"recipient refused", false, true, "rcpt to", "RCPT TO"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := &relay{rejectMailFrom: c.rejectMailFrom, rejectRcpt: c.rejectRcpt}
			port := r.serve(t)

			err := New(configFor("127.0.0.1", port, "none")).Send("user@example.org", "s", "b")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Send() = %v, want the %q step to be named", err, c.want)
			}
			if _, _, _, data := r.snapshot(); data != "" {
				t.Errorf("%s was refused but a message still reached the relay", c.step)
			}
		})
	}
}

func TestSendReportsUnreachableRelay(t *testing.T) {
	// Reserve a port and release it again: nothing listens there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	err = New(configFor("127.0.0.1", port, "none")).Send("user@example.org", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "dial 127.0.0.1:") {
		t.Fatalf("Send() = %v, want a dial error naming the address", err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Errorf("error %v does not wrap the network error, so callers cannot inspect it", err)
	}
}

func TestSendReportsSilentRelay(t *testing.T) {
	// A relay that accepts the connection and never greets: Send must report the
	// failed handshake rather than pretending the message went out.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close() // drop without a greeting
		}
	}()

	err = New(configFor("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, "none")).
		Send("user@example.org", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "smtp client") {
		t.Fatalf("Send() = %v, want an smtp client error", err)
	}
}
