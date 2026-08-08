# Firma GPG de artefactos de release

Cada release de onecloudriver en GitHub publica artefactos firmados con una
clave GPG dedicada. El workflow de release (`.github/workflows/release.yml`)
importa la clave privada desde los secrets del repositorio, genera un manifest
de checksums `SHA256SUMS`, firma el manifest y cada artefacto, y sube todo a la
página de la release:

| Asset | Significado |
|---|---|
| `SHA256SUMS` | checksums sha256 de todos los artefactos |
| `SHA256SUMS.asc` | firma GPG separada del manifest |
| `<artefacto>.asc` | firma GPG separada de cada artefacto |
| `public.key` | la clave pública de firma de releases |

Las instrucciones de verificación para usuarios finales están en el
[README](../README.es.md) ("Verificación de artefactos de release").

## Configuración actual

- **Clave**: RSA 4096, sin expiración
  - Fingerprint: `BF7FEBD77EE5436CE449D6CDB8FD5F3C8500FADC`
  - Identidad: `onecloudriver Release Signing <frosado@users.noreply.github.com>`
- **Secrets del repositorio** (Settings → Secrets and variables → Actions):
  - `GPG_PRIVATE_KEY` — clave privada en formato ASCII-armored (`gpg --armor --export-secret-keys`)
  - `GPG_PASSPHRASE` — la passphrase de la clave
- **Backup local**: `~/.onecloudriver-signing/`
  (`onecloudriver-release-signing.asc`, `.passphrase`, `.pub` — ¡guárdalo a salvo!)

> El workflow **falla de forma ruidosa** si faltan los secrets: una release nunca
> puede publicarse sin firmar por accidente.

## Configuración desde cero (nuevo mantenedor / nuevo repo)

```bash
# 1. Generar una clave de firma dedicada (batch, con passphrase)
PASSPHRASE=$(openssl rand -base64 24)
cat > /tmp/keygen <<EOF
Key-Type: RSA
Key-Length: 4096
Name-Real: onecloudriver Release Signing
Name-Email: <tu>@users.noreply.github.com
Expire-Date: 0
Passphrase: ${PASSPHRASE}
%commit
EOF
gpg --batch --gen-key /tmp/keygen && rm -f /tmp/keygen

# 2. Exportar la clave privada (armored) y la pública
FP=$(gpg --list-secret-keys --with-colons <email> | awk -F: '/^fpr/{print $10; exit}')
gpg --batch --pinentry-mode loopback --passphrase "$PASSPHRASE" \
    --armor --export-secret-keys "$FP" > release-signing.asc
gpg --armor --export "$FP" > public.key

# 3. Guardar los secrets (requiere admin en el repo)
gh secret set GPG_PRIVATE_KEY --repo <owner>/onecloudriver < release-signing.asc
gh secret set GPG_PASSPHRASE --repo <owner>/onecloudriver --body "$PASSPHRASE"

# 4. Haz backup local de la privada + passphrase (permisos 600) y borra la
#    exportación en texto plano una vez configurados los secrets.
# 5. Verifica:
gh secret list --repo <owner>/onecloudriver
```

## Recuperación

Si hay que restaurar la clave (máquina nueva, keyring perdido):

```bash
gpg --import ~/.onecloudriver-signing/onecloudriver-release-signing.asc
# la passphrase está en ~/.onecloudriver-signing/onecloudriver-release-signing.passphrase
gpg --list-keys onecloudriver
```

## Rotación

Si la clave está comprometida o simplemente quieres una nueva:

1. Genera una clave nueva y actualiza los secrets (ver "Configuración desde cero").
2. Publica la nueva `public.key` en la próxima release — el workflow la exporta
   automáticamente desde la clave importada.
3. **Las releases antiguas siguen siendo verificables**: cada release incluye su
   `public.key` propia, así que los usuarios pueden verificar artefactos viejos.
4. Opcionalmente revoca la clave antigua: `gpg --gen-revoke <old-fp>`.

## Notas de seguridad

- **Nunca subas** la clave privada ni la passphrase al repositorio.
- La passphrase se pasa a `gpg` por stdin (`--passphrase-fd 0`) en el workflow
  para que nunca aparezca en la lista de procesos.
- `Good signature` solo prueba que el archivo fue firmado con esa clave — los
  usuarios deben confirmar el fingerprint por un canal de confianza.
- El directorio de backup (`~/.onecloudriver-signing/`) debería incluirse en
  tus copias de seguridad habituales o copiarse a un gestor de contraseñas.

## Opcional: publicar la clave pública en tu perfil de GitHub

La página de release ya incluye `public.key`, así que no es obligatorio. Si
además quieres que la clave aparezca en tu perfil de GitHub (como clave
verificada):

1. `gh auth refresh -h github.com -s admin:gpg_key` (otorga el scope de la API)
2. `gh api -X POST user/gpg_keys -f armored_public_key="$(cat public.key)"`
3. O añádela a mano: GitHub → Settings → SSH and GPG keys → **New GPG key**.
