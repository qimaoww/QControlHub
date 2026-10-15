# Account authentication across panel upgrades

## Findings and affected code

Database account creation and password reset bcrypt-hash the original UTF-8
bytes. Before this fix, `/api/v1/auth/login` called `strings.TrimSpace` on the
submitted password. Leading/trailing ASCII or Unicode whitespace therefore made
a successfully created/reset password unusable. Even the trimmed password does
not match that stored hash. The defect is present from the first database-user
release, commit `7502d9b9` (2026-08-21), through the tested pre-fix main commit
`ba77e7ed`. This is a pre-existing login defect exposed by reauthentication, not
evidence that an upgrade rewrote a previously working password.

Two other observed failures are fixed:

- bcrypt comparison can accept a suffix after the maximum 72 password bytes.
  Login now rejects those inputs after paying the comparison cost. Creation and
  reset still require valid UTF-8 and 12-72 **bytes**. Login does not reapply the
  current creation minimum to historical hashes.
- Database lookup failures previously became the same 401 as wrong credentials
  and consumed the authentication-failure budget. They now produce a generic
  500 and a contextual server diagnostic, without recording a bad-password
  attempt. Missing/disabled accounts and incorrect passwords retain the same
  public 401, dummy bcrypt comparison for missing/disabled accounts, and limiter.

The login form no longer applies a 12-character minimum: four Chinese characters
can already meet the server's 12-byte requirement. Login accepts the exact form
value, including whitespace. Case-insensitive username lookup follows the
existing `lower(username)` unique index, also supporting mixed-case historical
records without changing their stored names or IDs.

## Credential and session boundaries

| Value | Purpose | Upgrade behavior |
| --- | --- | --- |
| Database password | Compare against `panel_users.password_hash` | Preserve original bytes and existing hash; never trim or rehash on login/startup |
| Environment administrator token | Independent emergency administrator credential | Preserve existing token whitespace tolerance; a successful token login does not validate any database account |
| Configuration encryption key | Encrypt/decrypt configuration content | Does not hash or verify account passwords; retain the key/keyring to keep configuration data readable |
| Session cookie | Reference an in-memory authenticated session | Restart loses sessions; users must authenticate again with their original credentials |

Database accounts named `admin` still authenticate as their durable user ID and
workspace. Environment-token fallback remains available under `admin` or an empty
username, including during database-account lookup failure. Server logs diagnose
that failure even when token fallback succeeds. No passwords, hashes, tokens or
usernames are added to those diagnostics.

Password resets, disabling, role/permission changes and existing profile updates
continue to revoke sessions through `auth_revision` and local session removal.
Updating display name or permissions without submitting a password preserves
the stored hash byte for byte.

## Verification

`make panel-auth-upgrade-test` builds the real historical control-plane at
`e3ff68389cc75d6a29c52822bdd31af3bcd0ab23` (schema v58) and the current source.
It needs Git history, Go, and `QCH_TEST_DATABASE_URL` pointing to a test PostgreSQL
database. It creates and removes an isolated schema; it never uses deployment
credentials. The Debian CI job runs this target with PostgreSQL 17.

The lifecycle test:

1. Creates ordinary users, a database administrator named `admin`, reset,
   disabled, whitespace-password and Chinese-password accounts using the old
   executable's HTTP API. Confirms working accounts log in before upgrade and
   reproduces the whitespace-password 401 on that executable.
2. Creates a user-owned configuration and records account metadata in memory.
3. Injects a transactional migration failure, checks that credentials and the
   schema ledger remain unchanged, and verifies the old executable can restart
   and authenticate its original ordinary/admin accounts.
4. Upgrades the same PostgreSQL schema from v58 to v72, then restarts the current
   executable twice more. Each run rejects old cookies, accepts original
   passwords in fresh cookie jars, checks IDs/workspaces/roles/permissions and
   configuration ownership, and rejects wrong, reset-old and disabled passwords.
   Database administrator capabilities include newly introduced administrator
   permissions; its stored permission field is unchanged.
5. Compares ID, username, hash, disabled status, role, permissions and
   `auth_revision` without printing hashes or credentials.

The ordinary integration suite separately covers ASCII/Unicode whitespace,
all-space passwords previously accepted at creation, Chinese, combining Unicode,
special characters, byte boundaries, profile updates without passwords,
reset/disable revocation across server instances, database outages, token
fallback, and login limits. Store tests cover historical operator/auditor/readonly
role migration, mixed-case names, rollback, repeated migration and owned data.
Browser regression checks validate the login form's native validity for a
four-character Chinese password. Existing tests cover transport, origins, secure
cookies, CSRF and frontend request/session errors.

An additional local PostgreSQL 17/container check used the historical and current
web images with HTTPS nginx proxying. The ordinary account authenticated in the
old browser before both application containers were replaced. Its old cookie
then received a session-expiry 401. New browser contexts authenticated ordinary,
database-admin, whitespace-password and Chinese-password accounts with their
original credentials. The account-field comparison remained identical, and
secure cookie delivery and CSRF checks succeeded. Only fixture accounts were
used; no production database was inspected.

Audited upgrade paths preserve the bundled PostgreSQL volume/project and existing
database settings. External updates refuse to replace a configured database URL
and compare `.env` before and after update; failed updates retain configuration
and image rollback materials. These operational scripts need no password
rewrite. Their generated outputs are checked by `make deploy-modules-check`.
An image rollback after a **successful schema upgrade** is not a database
rollback: older executables reject a newer schema. Restore a matching database
backup if such a rollback is necessary.

## Diagnosis and recovery

No account-data corruption was reproduced: the historical upgrade and repeated
startup tests retain original password hashes and identity fields. These tests
do not establish the state of an installation whose database or logs were not
provided.

For an affected installation, first classify the actual response and stage:

- A 401 from `/auth/session` after restart is expected session expiry. Try a fresh
  browser session and the original username/password against `/auth/login`.
- A login 401 is deliberately uniform for wrong, unknown or disabled accounts.
  Check account metadata through authorized administration and compare database
  records privately with a verified pre-upgrade backup. Do not infer hash loss
  from the message or from a successful environment-token login.
- A login 500 requires the server's `lookup database account for login` diagnostic.
  Check database connectivity, selected database/schema and migration completion.
- A 429 indicates the existing login-failure limit. Correct the cause and wait
  for the configured block to expire.
- A login 200 followed immediately by session loss requires checking cookie
  delivery, HTTPS/proxy/origin configuration and both application image versions.
  Frontend network/proxy errors remain distinct from session-expiry feedback.
  The web image versions module URLs; HTML/API responses use `no-store`.

If an intact hash is present, deploy the fix and retry the **exact original
password**; no reset or hash migration is required. Do not trim stored secrets,
derive new passwords from hashes, or rotate configuration encryption keys to fix
account authentication.

If verified backups demonstrate actual damage, determine which IDs/fields
changed and preserve a backup of the current database first. Restore only records
whose identity and ownership can be verified, preserving user IDs and related
owner IDs. Preserve/bump `auth_revision` to invalidate sessions when restoring
authentication fields; do not restore a stale revision that could revive a
session. With no trustworthy hash backup, use the existing authorized password
reset flow for the affected account. Bcrypt hashes cannot recover plaintext
passwords. If the wrong database/schema or volume was selected, reconnect the
verified original data rather than creating replacement users with new IDs.
