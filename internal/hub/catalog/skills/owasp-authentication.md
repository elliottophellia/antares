---
name: owasp-authentication
description: Test an authorized application's authentication and session handling. Use during authorized web/API testing.
tags: [security, owasp, authentication, session, jwt]
triggers: [authentication, login, session, jwt, password reset, mfa]
---

# Authentication and sessions

Authentication proves who you are; session handling remembers it. Both are
frequent failure points. **Authorized targets only.**

## Authentication

- **Credential handling**: is the login rate-limited? Does it lock out? Are
  usernames enumerable through different responses or timing for valid versus
  invalid users?
- **Password reset**: is the token long, random, single-use, and expiring? Can
  the reset be pointed at another account? Does the reset response leak whether
  an email exists?
- **MFA**: can it be skipped by going straight to the post-login endpoint? Is
  the second factor rate-limited against brute force?

## Sessions

- **Token strength**: is the session id long and random, or guessable?
- **Fixation**: does the session id change on login? If not, a fixed id can be
  planted.
- **Lifetime**: does logout invalidate the token server-side, or only drop the
  cookie? Does the token expire?
- **Flags**: `HttpOnly`, `Secure`, and a sensible `SameSite` on the cookie.

## JWTs, when used

- **alg=none**: does the server accept a token with the signature removed and
  the algorithm set to none?
- **Weak secret**: is an HS256 token signed with a guessable secret?
- **Confusion**: does the server accept a token signed with the public key as
  an HMAC secret?
- **Claims**: is `exp` checked? Can a role claim be edited?

## Recording

The exact request, the flaw demonstrated, the impact — account takeover is
critical — and the fix.
