# Account email delivery

Email verification, password recovery, email changes and security notices are
optional. Existing deployments keep mail disabled until an operator prepares the
relay, creates `.env.email` and enables delivery. This change does not configure a real relay,
send a test email or establish production deliverability.

The public application URL must be the exact HTTPS browser origin, with no
credentials, path, query or fragment. An IP origin needs a certificate valid for
that IP. HTTP is accepted only for explicit loopback development origins.
Proofs appear in URL fragments and expire; opening a link alone does not complete
an action or log the visitor in. A reset request does not change credentials or
end sessions. Successful password reset and email change end previous sessions
and require a fresh login.

Prepare the VPS files before enabling the overlay:

1. Create `/opt/vault-chat/secrets/mail` with owner `65532:65532` and mode `0700`.
2. Generate a dedicated outbox encryption key on the deployment host. The file
   `outbox.key` must contain 32 random raw bytes or 64 hexadecimal digits. For
   example, an operator can use `umask 077` and
   `openssl rand -hex 32 > secrets/mail/outbox.key` from the checkout.
   Never reuse the JWT signing key, a password or an example key.
3. Enter the relay password into `secrets/mail/smtp_password` with a private
   editor, avoiding shell history and environment variables. An optional CA
   bundle belongs in `secrets/mail/ca.pem`.
4. Set owner `65532:65532` and mode `0400` on these files. Auth runs as UID 65532;
   a root-owned `0600` file is not readable by it. World permission bits must be
   zero. Do not mount the parent secrets directory or expose these files to the
   other services.
5. Copy `deployments/email.env.example` to `.env.email`, mode `0600`, and edit the
   public URL, relay address, sender and relay username. The example uses reserved
   domains and cannot deliver mail. Keep the actual password and key in files.

The presence of `.env.email` makes the VPS helper add
`deployments/docker-compose.email.yml`. The overlay configures mail only on Auth,
mounts the narrow directory read-only with automatic host-directory creation
disabled, and adds a dedicated bridge network for outbound SMTP. Auth keeps
its existing RPC, data and events networks; no new host ports are published.
Allow outbound connections to the chosen relay port in the host/provider policy.

Set `AUTH_SMTP_TLS_MODE=starttls` for mandatory STARTTLS, usually on port 587, or
`tls` for implicit TLS, usually on port 465. Certificates and hostnames are always
verified, with TLS 1.2 or newer. A custom CA bundle supplements the system trust
store; it never disables verification. Configured credentials require AUTH PLAIN
over verified TLS. For an operator-controlled relay without authentication, leave
both `AUTH_SMTP_USERNAME` and `AUTH_SMTP_PASSWORD_FILE` explicitly empty. Mail
delivery remains encrypted. Set `AUTH_SMTP_CA_FILE=/run/secrets/mail/ca.pem` only
when using the optional file.

`AUTH_EMAIL_LEGACY_DEADLINE` is an optional RFC3339 transition deadline for legacy
accounts. Do not set it until recovery and verification delivery have been
validated: enforcing ownership without working mail can lock users out.
For a legacy account with an incorrect email or a lost password and no verified
address, use an operator-assisted recovery process with independent identity
checks before setting the deadline. A mailbox holder alone cannot reclaim that
account: resend requires its current password and reset requires prior email
verification. Do not bulk-mark existing addresses verified to bypass enrollment.
New invited accounts use the verification flow; existing accounts have no
implicit deadline. Do not remove the opt-in as a way to bypass ownership checks.
For temporary relay maintenance, keep `.env.email` and set
`AUTH_MAIL_ENABLED=false`. The overlay defaults to true when the setting is
absent. Leave the public URL and SMTP fields populated because Compose still
requires them when rendering configuration. Auth skips reading the key/password
and does not start its mail worker while delivery is disabled, but it keeps the
configured legacy deadline and image compatibility guard.

Removing `.env.email` also removes its configured legacy deadline from Auth,
so use the maintenance setting when that deadline must remain enforced.
Persisted ownership requirements in the database are not removed. Preserve
`secrets/mail/outbox.key`: its presence records that this deployment needs an
email-aware Auth image even without the delivery file. Preparing the key
conservatively enables the compatibility guard before the first message is sent.

When `.env.email` or the preserved outbox key exists, `up`, `update`, `install`
and `restore` refuse Auth images whose healthcheck lacks `-check-account-email`.
This capability check uses no network, environment or
secret mounts; it does not prove that a relay is reachable or mail is accepted.
The existing mTLS checks still run. Update checks occur before backup/pause or
image-selector changes; install checks precede host changes; restore checks the
backed-up opt-in or key before extracting live configuration or creating data volumes.
While either marker exists, the helper pins the image selector to `.env.vps`; ambient
variables and `.env.email` cannot select a different image after the guard.
The guard has a 15-second deadline and a five-second termination grace.

For local Compose, explicitly supply `--env-file .env.email` and both
`-f deployments/docker-compose.yml -f deployments/docker-compose.email.yml`.
The normal development stack remains unchanged without the overlay. For host
processes, use the disabled-by-default settings in `.env.example` and file paths
relative to the checkout.

Mail contents and bearer proofs are encrypted in the transactional outbox.
Each worker sends one message at a time, with a ten-second network deadline,
bounded replies and at most five claimed attempts. Delivery is at least once:
an SMTP acceptance followed by a lost database acknowledgement can cause a
duplicate. Changing the encryption key does not re-encrypt pending jobs; they
become unreadable and are discarded. Drain the queue or plan an explicit discard
before rotating the key. Never regenerate it during routine image updates.
Do not run the email migration's down script as a routine application rollback:
it removes verification and credential-version state. Keep a compatible Auth
image and roll forward with a reviewed fix instead.

Encrypted configuration backups include `.env.email`, the overlay and the mail
directory alongside the corresponding database snapshot. Preserve that key when
restoring pending mail; restore repairs UID 65532 ownership and private modes.
The key remains in configuration backups when delivery is paused, and its
compatibility guard and private ownership are restored without enabling SMTP.
Never delete the key to permit a downgrade: an older Auth image can ignore
ownership policy stored in the database. This local marker depends on operators
preserving it; it does not independently attest the database's policy history.
Restore requires a fresh checkout without existing runtime configuration.
These local synthetic regressions do not prove a remote backup destination,
off-host recovery, SMTP delivery or SPF/DKIM/DMARC configuration. An operator must
verify those separately with the chosen provider and authorized recipients.
