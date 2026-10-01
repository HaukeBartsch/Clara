package admin

// The first-factor handle — how a two-factor challenge completes a local login
// without the user typing their password twice and without anyone storing it.
//
// `POST /api/v1/auth/login` verifies the bcrypt hash on every call for
// source "local" (Sequence C step 0, REQ-AUTH-050) and the API holds no
// challenge state of its own (GD-1), so the second submission a challenged
// user makes has to carry a first-factor proof. The alternatives were worse:
// keeping the password in the PHP session is plaintext persistence of a
// credential, which REQ-AUTH-036 forbids outright; asking again on the code
// panel contradicts `User_Interface_Design.md` §2.2's single code field.
//
// So verify-password — which has just verified the hash and does nothing else
// (REQ-API-123) — issues a short-lived signed statement to that effect, and
// login accepts it in place of `password`. It is stateless by construction:
// the proof is an HMAC under a key derived from the internal service secret,
// so nothing new is kept anywhere (§2.7's "the API keeps no challenge state"
// still holds — the state lives in `user_two_factor` and in this signature).
//
// What it is not: a general credential. It is issued only for an account a
// second factor still guards (method other than `off`, or the installation-wide
// mandate of REQ-AUTH-055/REQ-CFG-027 standing), so presenting one can never
// finish a login on its own — gate 1.5 still has to pass. It travels only on
// the internal boundary, is bound to one user id and address, and expires with
// the pending-session TTL it exists to survive. Like every other secret here it
// never appears in a log line or an audit detail (REQ-AUTH-036).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"csms/api/internal/db"
)

const (
	// firstFactorTTL is how long an issued handle is accepted. It matches the
	// tfa_pending lifetime of Authentication_Authorization_Design.md §3: the
	// handle outlives nothing the session holding it does not.
	firstFactorTTL = 5 * time.Minute

	// firstFactorVersion tags the payload format so a future change cannot be
	// read as an old one.
	firstFactorVersion = "ff1"

	// firstFactorContext derives the signing key from the service secret
	// (REQ-CFG-013) instead of using that secret directly: a handle is not
	// proof of the caller's identity, and a key used for one purpose should not
	// be reused for another.
	firstFactorContext = "clara/first-factor/v1"
)

// issueFirstFactor signs the handle for u. The payload carries what
// acceptFirstFactor re-checks — and nothing that identifies a session, because
// there is none (GD-1). It is JSON under base64url rather than delimited text:
// an address contains dots, and any separator one can guess is one an address
// can eventually break.
func (h *Handler) issueFirstFactor(u *db.User, at time.Time) string {
	payload, err := json.Marshal(firstFactorClaims{
		Version: firstFactorVersion,
		UserID:  u.ID,
		Expires: at.Add(firstFactorTTL).Unix(),
	})
	if err != nil {
		// Unreachable for three scalar fields; issuing nothing is the honest
		// answer if it ever happened, since a handle that cannot be signed is a
		// challenge that cannot be completed.
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(h.firstFactorMac(payload))
}

// acceptFirstFactor reports whether handle is this API's own, unexpired proof
// for exactly this account. Any structural problem, mismatch or expiry answers
// false: the caller has one response for all of them, because a caller holding
// a stale handle gains nothing by learning which check failed.
func (h *Handler) acceptFirstFactor(handle string, u *db.User, at time.Time) bool {
	encoded, signature, found := strings.Cut(handle, ".")
	if !found {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	// The signature is checked over the decoded bytes, before they are read:
	// nothing unauthenticated reaches the parser.
	if subtle.ConstantTimeCompare(given, h.firstFactorMac(payload)) != 1 {
		return false
	}

	var claims firstFactorClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	if claims.Version != firstFactorVersion || claims.UserID != u.ID {
		return false
	}

	return claims.Expires > at.Unix()
}

// firstFactorClaims is the signed statement: this API verified user UserID's
// first factor, valid until Expires (unix seconds). The address is deliberately
// absent — the id is the identity login resolved that email to, so a handle
// cannot be pointed at another account without failing this check.
type firstFactorClaims struct {
	Version string `json:"v"`
	UserID  int64  `json:"uid"`
	Expires int64  `json:"exp"`
}

// secondFactorGuards reports whether a second factor stands between this
// account and a session: its own method, or the installation-wide mandate that
// sends an `off` account into enrollment before login completes (REQ-AUTH-055).
// It is the same question gate 1.5 asks, which is the point — a handle is only
// ever accepted where that gate would still close behind it.
func (h *Handler) secondFactorGuards(ctx context.Context, u *db.User) (bool, error) {
	if h.Cfg.AuthRequire2FA {
		// The mandate sends an `off` account into enrollment before login
		// completes, which is a gate the handle cannot walk through.
		return true, nil
	}
	tfa, err := h.Store.GetTFA(ctx, u.ID)
	if err != nil {
		return false, err
	}

	return tfa != nil && tfa.Method != "off", nil
}

func (h *Handler) firstFactorMac(payload []byte) []byte {
	mac := hmac.New(sha256.New, h.firstFactorKey())
	mac.Write(payload)

	return mac.Sum(nil)
}

func (h *Handler) firstFactorKey() []byte {
	deriver := hmac.New(sha256.New, []byte(h.Cfg.InternalServiceToken))
	deriver.Write([]byte(firstFactorContext))

	return deriver.Sum(nil)
}
