---
name: owasp-access-control
description: Test an authorized web application for broken access control — the most common serious web flaw. Use during authorized web testing.
tags: [security, owasp, web, authorization]
triggers: [access control, authorization, idor, privilege, bola]
---

# Broken access control

Access control decides who may do what. When it is broken, a user reaches data
or actions that should not be theirs. It is the most common serious web flaw, so
test it first and test it thoroughly. **Only against a target in your authorized
scope.**

## Object-level (IDOR / BOLA)

The classic: an identifier in a request that the server trusts without checking
ownership.

1. Find requests that reference an object by id — `/api/orders/1043`,
   `?doc=88`, a hidden form field.
2. As a low-privilege account, change the id to one you should not see. Does the
   server return it, or refuse?
3. Try adjacent ids, ids from another account you control, and non-numeric or
   out-of-range values.
4. Confirm before reporting: fetch the same object as its rightful owner and
   show the two responses match. That is the difference between a finding and a
   guess.

Do not enumerate real user data to prove access — demonstrate one case and
describe the rest.

## Function-level

Can a normal user reach an admin action?

- Take an action as an admin, note the exact request, then replay it as a
  normal user. Removing a role from the UI does not remove the endpoint.
- Look for `/admin`, `/internal`, and API methods (DELETE, PUT) the UI hides
  but the server still honours.

## Horizontal and vertical

- **Horizontal**: reaching a peer's data (user A sees user B's invoice).
- **Vertical**: reaching a higher privilege (a user performs an admin action).

Test both. A system can get one right and the other wrong.

## Recording

For each finding: the exact request, the account it was made from, what came
back that should not have, the impact, and the fix — enforce authorization on
the server for every object and every action, never on the client.
