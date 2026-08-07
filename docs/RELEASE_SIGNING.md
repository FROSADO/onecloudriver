# Release artifact signing (GPG)

Every GitHub release of onecloudriver ships artifacts signed with a dedicated
GPG key. The release workflow (`.github/workflows/release.yml`) imports the
private key from repository secrets, generates a `SHA256SUMS` checksum
manifest, signs the manifest and every artifact, and uploads everything to the
release page:

| Asset | Meaning |
|---|---|
| `SHA256SUMS` | sha256 checksums of every artifact |
| `SHA256SUMS.asc` | detached GPG signature of the manifest |
| `<artifact>.asc` | detached GPG signature of each artifact |
| `public.key` | the release signing public key |

End-user verification instructions live in the main
[README](../README.md) ("Verifying release artifacts").

## Current configuration

- **Key**: RSA 4096, no expiry
  - Fingerprint: `BF7FEBD77EE5436CE449D6CDB8FD5F3C8500FADC`
  - Identity: `onecloudriver Release Signing <frosado@users.noreply.github.com>`
- **Repository secrets** (Settings → Secrets and variables → Actions):
  - `GPG_PRIVATE_KEY` — armored private key (`gpg --armor --export-secret-keys`)
  - `GPG_PASSPHRASE` — the key's passphrase
- **Local backup**: `~/.onecloudriver-signing/`
  (`onecloudriver-release-signing.asc`, `.passphrase`, `.pub` — keep it safe!)

> The workflow **fails loudly** if the secrets are missing: a release can never
> be published unsigned by accident.

## Setup from scratch (new maintainer / new repo)

```bash
# 1. Generate a dedicated signing key (batch, with passphrase)
PASSPHRASE=$(openssl rand -base64 24)
cat > /tmp/keygen <<EOF
Key-Type: RSA
Key-Length: 4096
Name-Real: onecloudriver Release Signing
Name-Email: <you>@users.noreply.github.com
Expire-Date: 0
Passphrase: ${PASSPHRASE}
%commit
EOF
gpg --batch --gen-key /tmp/keygen && rm -f /tmp/keygen

# 2. Export the private key (armored) and the public key
FP=$(gpg --list-secret-keys --with-colons <email> | awk -F: '/^fpr/{print $10; exit}')
gpg --batch --pinentry-mode loopback --passphrase "$PASSPHRASE" \
    --armor --export-secret-keys "$FP" > release-signing.asc
gpg --armor --export "$FP" > public.key

# 3. Store the secrets (requires admin on the repo)
gh secret set GPG_PRIVATE_KEY --repo <owner>/onecloudriver < release-signing.asc
gh secret set GPG_PASSPHRASE --repo <owner>/onecloudriver --body "$PASSPHRASE"

# 4. Back up the private key + passphrase locally (600 perms) and delete the
#    plaintext export once the secrets are set.
# 5. Verify:
gh secret list --repo <owner>/onecloudriver
```

## Recovery

If the key needs to be restored (new machine, lost keyring):

```bash
gpg --import ~/.onecloudriver-signing/onecloudriver-release-signing.asc
# the passphrase is in ~/.onecloudriver-signing/onecloudriver-release-signing.passphrase
gpg --list-keys onecloudriver
```

## Rotation

If the key is compromised or you simply want a new one:

1. Generate a new key and set the secrets (see "Setup from scratch").
2. Publish the new `public.key` in the next release — the workflow exports it
   automatically from the imported key.
3. **Old releases stay verifiable**: the old `public.key` was uploaded with
   each release, so users can still verify old artifacts.
4. Optionally revoke the old key: `gpg --gen-revoke <old-fp>`.

## Security notes

- **Never commit** the private key or the passphrase to the repository.
- The passphrase is fed to `gpg` via stdin (`--passphrase-fd 0`) in the
  workflow so it never appears in the process list.
- `Good signature` only proves the file was signed by that key — users should
  confirm the fingerprint via a trusted channel.
- The backup directory (`~/.onecloudriver-signing/`) should be included in
  your regular backups or copied to a password manager.

## Optional: publish the public key on your GitHub profile

The release page already ships `public.key`, so this is not required. If you
also want the key listed on your GitHub account profile (as a verified key):

1. `gh auth refresh -h github.com -s admin:gpg_key` (grants the API scope)
2. `gh api -X POST user/gpg_keys -f armored_public_key="$(cat public.key)"`
3. Or add it manually: GitHub → Settings → SSH and GPG keys → **New GPG key**.
