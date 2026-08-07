#!/usr/bin/env bash
# =============================================================================
# release.sh — Automates the GitHub release process for OneCloudRiver.
#
# Flow (full mode):
#   1. Pre-flight: checklist of everything to review before releasing
#      (gh auth, working tree, unpushed commits, open PRs, CI status,
#      last tag, CHANGELOG status, README/docs references).
#   2. Optional: squash-merge the open PRs (gh pr merge) and switch to the
#      default branch so the release is published from there.
#   3. Interactive version selection (proposes the next patch).
#   4. Auto-drafts the CHANGELOG section from commits since the last tag,
#      then opens $EDITOR so you can review and adjust it.
#   5. Optional: update version references in README.md and the docs
#      (docs/MANUAL*.md, man pages).
#   6. Commits, creates the annotated tag vX.Y.Z and pushes branch + tag.
#   7. The existing .github/workflows/release.yml builds the artifacts
#      (zip, .deb, .rpm) and creates the GitHub Release automatically.
#   8. Monitors the workflow run and verifies the release.
#
# Usage:
#   bash scripts/release.sh            # full interactive flow
#   bash scripts/release.sh --check    # only the pre-flight checklist (read-only)
#   bash scripts/release.sh --yes      # auto-confirm prompts (editor still opens)
#   bash scripts/release.sh --help
#
# Requirements: git + GitHub CLI (gh) authenticated.
# =============================================================================

set -euo pipefail

# -----------------------------------------------------------------------------
# Configuration
# -----------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

CHANGELOG="CHANGELOG.md"
REMOTE="origin"
RELEASE_WORKFLOW="release.yml"

# Documentation files that may carry version references (checked in step 5).
DOC_VERSION_PATTERNS=('docs/MANUAL*.md' 'docs/onecloudriver.1*')

MODE="full"
ASSUME_YES=0
REPO=""
CURRENT_BRANCH=""
DEFAULT_BRANCH=""
LAST_TAG=""
NEW_VERSION=""
NEW_TAG=""

# -----------------------------------------------------------------------------
# Colors & logging
# -----------------------------------------------------------------------------
if [[ -t 1 ]]; then
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'
  C_BLUE=$'\033[34m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
  C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""; C_BOLD=""; C_RESET=""
fi

info() { printf '%b\n' "${C_BLUE}ℹ️${C_RESET} $*"; }
ok()   { printf '%b\n' "${C_GREEN}✅${C_RESET} $*"; }
warn() { printf '%b\n' "${C_YELLOW}⚠️${C_RESET} $*"; }
err()  { printf '%b\n' "${C_RED}❌${C_RESET} $*"; }
die()  { err "$*"; exit 1; }
hr()   { printf '%b\n' "${C_BOLD}──────────────────────────────────────────${C_RESET}"; }

usage() {
  cat <<EOF
Uso: bash scripts/release.sh [opciones]

Automatiza el proceso de release de OneCloudRiver en GitHub:
  pre-flight checks → merge de PRs (opcional) → versión → CHANGELOG
  (borrador desde commits) → commit + tag vX.Y.Z → push (dispara el
  workflow Release) → monitorización.

Opciones:
  --check   Solo muestra el checklist pre-release (no modifica nada).
  --yes     Auto-confirma los prompts sí/no (el editor sigue abriéndose).
  --help    Muestra esta ayuda.
EOF
}

# confirm: $1 = prompt. Returns 0 if confirmed.
confirm() {
  local ans
  if [[ "${ASSUME_YES}" -eq 1 ]]; then return 0; fi
  read -r -p "$1 [s/N] " ans || true
  [[ "${ans,,}" == "s" || "${ans,,}" == "y" || "${ans,,}" == "si" ]]
}

# -----------------------------------------------------------------------------
# Pre-flight checks (the "everything to review" checklist)
# -----------------------------------------------------------------------------
CHECKS_FAILED=0

check_gh() {
  local ok_=1
  if ! command -v gh >/dev/null 2>&1; then
    err "gh CLI no está instalado. Instálalo: https://cli.github.com/"
    CHECKS_FAILED=1
    ok_=0
  elif ! gh auth status >/dev/null 2>&1; then
    err "gh CLI no está autenticado. Ejecuta: gh auth login"
    CHECKS_FAILED=1
    ok_=0
  fi
  [[ "${ok_}" -eq 1 ]] || return 0
  local user
  user="$(gh api user --jq .login 2>/dev/null || echo '?')"
  ok "gh CLI instalado y autenticado (${user})"
}

detect_repo() {
  local url
  url="$(git remote get-url "${REMOTE}" 2>/dev/null || true)"
  if [[ -z "${url}" ]]; then
    err "No se encontró el remoto '${REMOTE}'."
    CHECKS_FAILED=1
    return 0
  fi
  # git@github.com-frosado:FROSADO/onecloudriver.git → FROSADO/onecloudriver
  # https://github.com/FROSADO/onecloudriver.git      → FROSADO/onecloudriver
  REPO="$(printf '%s' "${url}" |
    sed -E 's#^[^:@/]+@[^:]+:##; s#^[a-z]+://[^/]+/##; s#\.git$##')"
  export GH_REPO="${REPO}"
  ok "Repositorio: ${REPO}"
}

check_git_state() {
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    err "No estás dentro de un repositorio git."
    CHECKS_FAILED=1
    return 0
  fi
  CURRENT_BRANCH="$(git branch --show-current 2>/dev/null || true)"
  if [[ -z "${CURRENT_BRANCH}" ]]; then
    warn "Estás en estado detached HEAD. Se recomienda estar en una rama."
  else
    ok "Rama actual: ${CURRENT_BRANCH}"
  fi

  local dirty
  dirty="$(git status --porcelain)"
  if [[ -n "${dirty}" ]]; then
    warn "El working tree tiene cambios sin commitear:"
    printf '%s\n' "${dirty}" | sed 's/^/    /'
  else
    ok "Working tree limpio."
  fi

  # Commits sin push
  local upstream=""
  upstream="$(git rev-parse --abbrev-ref --symbolic-full-name "@{upstream}" 2>/dev/null || true)"
  if [[ -z "${upstream}" ]]; then
    warn "La rama '${CURRENT_BRANCH}' no tiene upstream configurado."
  else
    local unpushed
    unpushed="$(git log --oneline "${upstream}..HEAD" 2>/dev/null || true)"
    if [[ -n "${unpushed}" ]]; then
      warn "Hay commits locales sin hacer push a ${upstream}:"
      printf '%s\n' "${unpushed}" | sed 's/^/    /'
    else
      ok "La rama está sincronizada con ${upstream}."
    fi
  fi
}

check_prs() {
  local prs
  prs="$(gh pr list --state open --limit 10 \
    --json number,title,headRefName,isDraft \
    --jq '.[] | "#\(.number) (\(.headRefName)): \(.title)"' 2>/dev/null || true)"
  if [[ -z "${prs}" ]]; then
    ok "No hay PRs abiertas."
    return 0
  fi
  warn "PRs abiertas en ${REPO}:"
  printf '%s\n' "${prs}" | sed 's/^/    /'
  info "Recuerda mergear las PRs que deban entrar en esta release — el script puede hacerlo (paso opcional)."
}

check_ci() {
  if [[ -z "${CURRENT_BRANCH}" ]]; then
    return 0
  fi
  local runs
  runs="$(gh run list --branch "${CURRENT_BRANCH}" --limit 3 \
    --json databaseId,workflowName,status,conclusion,headSha \
    --jq '.[] | "\(.workflowName) [\(.status)\(.conclusion != null and .conclusion != "" ? "/" + .conclusion : "")]"' \
    2>/dev/null || true)"
  if [[ -z "${runs}" ]]; then
    info "No hay runs de CI recientes en la rama '${CURRENT_BRANCH}'."
    return 0
  fi
  info "CI reciente en '${CURRENT_BRANCH}':"
  printf '%s\n' "${runs}" | sed 's/^/    /'
  if printf '%s' "${runs}" | grep -q 'failure\|cancelled\|timed_out'; then
    warn "Hay runs de CI con errores. Revísalos antes de publicar."
  fi
}

get_last_tag() {
  git fetch --tags --quiet 2>/dev/null || true
  LAST_TAG="$(git describe --tags --abbrev=0 2>/dev/null || true)"
  if [[ -z "${LAST_TAG}" ]]; then
    LAST_TAG="$(gh release list --limit 1 --json tagName --jq '.[0].tagName' 2>/dev/null || true)"
  fi
  if [[ -z "${LAST_TAG}" ]]; then
    LAST_TAG="0.0.0"
    warn "No hay tags ni releases previas; se parte de 0.0.0."
  else
    ok "Último tag: ${LAST_TAG}"
  fi
}

# -----------------------------------------------------------------------------
# PR merge (optional step before publishing)
# -----------------------------------------------------------------------------
detect_default_branch() {
  DEFAULT_BRANCH="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name 2>/dev/null || true)"
  DEFAULT_BRANCH="${DEFAULT_BRANCH:-main}"
  ok "Rama por defecto: ${DEFAULT_BRANCH}"
}

# resolve_merge_choice: $1 = raw choice (lowercased), rest = open PR numbers.
# Echoes the selected PR numbers (one per line). Warnings go to stderr so
# they never pollute the captured output. Uses read -ra (no globbing) to
# split on commas and spaces.
resolve_merge_choice() {
  local choice="$1"
  shift
  local all=("$@")
  if [[ "${choice}" == "t" || "${choice}" == "all" || "${choice}" == "todas" ]]; then
    printf '%s\n' "${all[@]}"
    return 0
  fi
  local -a tokens=()
  local tok
  local -a out=()
  IFS=', ' read -ra tokens <<< "${choice}"
  for tok in "${tokens[@]}"; do
    [[ -n "${tok}" ]] || continue
    tok="${tok//#/}"
    if [[ "${tok}" =~ ^[0-9]+$ ]]; then
      out+=("${tok}")
    else
      warn "Se ignora '${tok}' (no es un número de PR)." >&2
    fi
  done
  printf '%s\n' "${out[@]}"
}

# merge_prs: offers to squash-merge the open (non-draft) PRs targeting the
# default branch, then refreshes the local default branch (switching to it if
# needed) so the release tag is created from the merged commits.
merge_prs() {
  local prs open_numbers choice numbers
  prs="$(gh pr list --state open --base "${DEFAULT_BRANCH}" --limit 20 \
    --json number,title,headRefName,isDraft,mergeable \
    --jq '.[] | select(.isDraft|not) | "#\(.number) (\(.headRefName)) [\(.mergeable)]: \(.title)"' \
    2>/dev/null || true)"
  if [[ -z "${prs}" ]]; then
    info "No hay PRs listas para mergear hacia '${DEFAULT_BRANCH}'."
    return 0
  fi
  hr
  printf '%b\n' "${C_BOLD}  PRs abiertas hacia ${DEFAULT_BRANCH}${C_RESET}"
  printf '%s\n' "${prs}" | sed 's/^/    /'
  hr
  open_numbers="$(printf '%s\n' "${prs}" | grep -oE '^#?[0-9]+' | tr -d '#')"

  if [[ "${ASSUME_YES}" -eq 1 ]]; then
    choice="t"
    info "Modo --yes: se mergearán todas las PRs abiertas."
  else
    read -r -p "¿Mergear alguna PR antes de publicar? (números separados por comas, 't' = todas, Enter = ninguna): " choice || true
    choice="${choice,,}"
  fi
  [[ -n "${choice}" ]] || { info "No se mergeará ninguna PR."; return 0; }

  numbers="$(resolve_merge_choice "${choice}" ${open_numbers})"

  local n failed=0 count=0 merged=0
  for n in ${numbers}; do
    count=$((count + 1))
    info "Mergeando PR #${n} (squash)..."
    # The merge result is captured so a failure does not trip set -e and sets
    # failed=1 (which triggers a confirmation before continuing).
    # GH_PROMPT_DISABLED avoids any gh interactive prompt hanging the flow.
    GH_PROMPT_DISABLED=1 gh pr merge "${n}" --squash && merged=1 || merged=0
    if [[ "${merged}" -eq 1 ]]; then
      ok "PR #${n} mergeada."
    else
      err "No se pudo mergear la PR #${n} (¿checks pendientes, conflictos o permisos?)."
      failed=1
    fi
  done

  if [[ "${count}" -eq 0 ]]; then
    warn "No se seleccionó ninguna PR válida."
    return 0
  fi
  if [[ "${failed}" -eq 1 ]] && ! confirm "Alguna PR no se pudo mergear. ¿Continuar la release igualmente?"; then
    die "Abortado. Resuelve las PRs pendientes y vuelve a ejecutar el script."
  fi

  # Refresh the local default branch so the release is built from the merged
  # commits (they only exist remotely after gh pr merge).
  if [[ "${CURRENT_BRANCH}" != "${DEFAULT_BRANCH}" ]]; then
    info "Las PRs se mergearon en '${DEFAULT_BRANCH}', pero estás en '${CURRENT_BRANCH}'."
    if confirm "¿Cambiar a '${DEFAULT_BRANCH}' y actualizarla para publicar desde ahí?"; then
      local switched=0
      if git show-ref --verify -q "refs/heads/${DEFAULT_BRANCH}"; then
        git checkout "${DEFAULT_BRANCH}" 2>/dev/null && switched=1 \
          || warn "No se pudo cambiar a '${DEFAULT_BRANCH}' (revisa tu working tree)."
      else
        git checkout -b "${DEFAULT_BRANCH}" --track "${REMOTE}/${DEFAULT_BRANCH}" 2>/dev/null && switched=1 \
          || warn "No se pudo crear la rama local '${DEFAULT_BRANCH}'."
      fi
      # The pull only runs if the checkout actually succeeded, so it can never
      # touch the wrong branch.
      if [[ "${switched}" -eq 1 ]]; then
        git pull --ff-only "${REMOTE}" "${DEFAULT_BRANCH}" 2>/dev/null \
          || warn "No se pudo actualizar '${DEFAULT_BRANCH}'. Haz el pull manualmente."
        CURRENT_BRANCH="$(git branch --show-current)"
        ok "Se publicará desde '${CURRENT_BRANCH}'."
      fi
    fi
  elif confirm "Ya estás en '${DEFAULT_BRANCH}'. ¿Actualizarla con las PRs mergeadas (git pull --ff-only)?"; then
    git pull --ff-only "${REMOTE}" "${DEFAULT_BRANCH}" 2>/dev/null \
      || warn "No se pudo actualizar '${DEFAULT_BRANCH}'. Haz el pull manualmente."
  fi
}

# -----------------------------------------------------------------------------
# Version helpers
# -----------------------------------------------------------------------------
# propose_version: from "v0.1.0" → "0.1.1"; from a pre-release "0.1.1-rc.1" → "0.1.1".
propose_version() {
  local last="${1#v}"
  local major minor patch
  if [[ "${last}" == *-* ]]; then
    printf '%s' "${last%%-*}"
    return 0
  fi
  IFS='.' read -r major minor patch <<<"${last}"
  printf '%s.%s.%s' "${major}" "${minor}" "$((10#${patch:-0} + 1))"
}

valid_version() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z]+)*$ ]]
}

check_version_availability() {
  local v="$1"
  if git rev-parse -q --verify "refs/tags/v${v}" >/dev/null 2>&1; then
    die "El tag v${v} ya existe localmente."
  fi
  if gh release view "v${v}" >/dev/null 2>&1; then
    die "Ya existe una release v${v} en GitHub."
  fi
  if grep -qE "^## \[${v}\]" "${CHANGELOG}" 2>/dev/null; then
    warn "CHANGELOG.md ya contiene una sección [${v}]. Se reutilizará."
  else
    ok "CHANGELOG.md no tiene sección para [${v}] (se creará)."
  fi
}

# -----------------------------------------------------------------------------
# CHANGELOG helpers
# -----------------------------------------------------------------------------
# draft_changelog: builds a draft section body from commits since the last tag,
# grouped by conventional-commit type (Keep a Changelog style).
draft_changelog() {
  local last="$1"
  local added=() fixed=() changed=() docs=() security=() other=()
  local hash subject body=""

  while read -r hash subject; do
    case "${subject}" in
      feat*)             added+=("${subject} — \`${hash}\`") ;;
      fix*)              fixed+=("${subject} — \`${hash}\`") ;;
      perf*|refactor*|style*) changed+=("${subject} — \`${hash}\`") ;;
      docs*)             docs+=("${subject} — \`${hash}\`") ;;
      security*)         security+=("${subject} — \`${hash}\`") ;;
      *)                 other+=("${subject} — \`${hash}\`") ;;
    esac
  done < <(git log --format='%h %s' --no-merges "${last}..HEAD" 2>/dev/null || true)

  # Appends a "### Heading" group of bullets to $body. printf -v preserves the
  # newlines, unlike $(...) which would strip trailing ones.
  add_group() {
    local heading="$1"
    shift
    [[ $# -gt 0 ]] || return 0
    printf -v body '%s%s\n' "${body}" "${heading}"
    local item
    for item in "$@"; do
      printf -v body '%s%s\n' "${body}" "- ${item}"
    done
    printf -v body '%s\n' "${body}"
  }

  add_group "### Added" "${added[@]}"
  add_group "### Fixed" "${fixed[@]}"
  add_group "### Changed" "${changed[@]}"
  add_group "### Documentation" "${docs[@]}"
  add_group "### Security" "${security[@]}"
  add_group "### Other" "${other[@]}"

  if [[ -z "${body}" ]]; then
    body=$'### Other\n- No hay cambios destacados.\n'
  fi
  printf '%s' "${body}"
}

insert_changelog_section() {
  local body new_section new_link
  if grep -qE "^## \[${NEW_VERSION}\]" "${CHANGELOG}"; then
    info "La sección [${NEW_VERSION}] ya existe en CHANGELOG.md; se conserva."
  else
    body="$(draft_changelog "${LAST_TAG}")"
    printf -v new_section '## [%s] - %s\n\n%s\n\n---\n\n' \
      "${NEW_VERSION}" "$(date +%F)" "${body}"
    # The section is passed via ENVIRON (not -v) so awk does not reinterpret
    # backslashes inside commit messages.
    SEC="${new_section}" awk '
      BEGIN { sec = ENVIRON["SEC"] }
      /^## \[/ && !inserted { printf "%s", sec; inserted=1 }
      { print }
      END { if (!inserted) printf "%s", sec }
    ' "${CHANGELOG}" > "${CHANGELOG}.tmp" && mv "${CHANGELOG}.tmp" "${CHANGELOG}"
  fi

  new_link="[${NEW_VERSION}]: https://github.com/${REPO}/releases/tag/${NEW_TAG}"
  if ! grep -qF "${new_link}" "${CHANGELOG}"; then
    awk -v link="${new_link}" '
      /^\[[^]]+\]: https?:\/\// && !inslink { print link; inslink=1 }
      { print }
      END { if (!inslink) print link }
    ' "${CHANGELOG}" > "${CHANGELOG}.tmp" && mv "${CHANGELOG}.tmp" "${CHANGELOG}"
  fi
}

# launch_editor: opens $EDITOR (fallback vi), tolerating flags like "code --wait".
launch_editor() {
  local editor="${EDITOR:-vi}"
  # || true: an editor exiting non-zero (e.g. vim :cq) should not abort the flow.
  if [[ "${editor}" == *" "* ]]; then
    eval "${editor} \"${CHANGELOG}\"" || true
  else
    "${editor}" "${CHANGELOG}" || true
  fi
}

# find_version_files: prints the docs (matching DOC_VERSION_PATTERNS) that
# contain the given version string.
find_version_files() {
  local needle="$1"
  local pattern f
  for pattern in "${DOC_VERSION_PATTERNS[@]}"; do
    for f in ${pattern}; do
      [[ -f "${f}" ]] && grep -qF "${needle}" "${f}" && printf '%s\n' "${f}"
    done
  done
  return 0
}

# version_files_with_ref: prints README.md (if it has references) and the docs
# matching DOC_VERSION_PATTERNS that contain the given version string.
version_files_with_ref() {
  local old="$1"
  [[ -f README.md ]] && grep -qF "${old}" README.md && printf 'README.md\n'
  find_version_files "${old}"
}

# check_version_refs: read-only listing used by the pre-flight checklist.
check_version_refs() {
  local old="${LAST_TAG#v}"
  local -a files=()
  local f
  while IFS= read -r f; do
    files+=("${f}")
  done < <(version_files_with_ref "${old}")
  if [[ "${#files[@]}" -eq 0 ]]; then
    ok "No hay referencias a la versión ${old} en README.md ni en docs/."
  else
    info "Referencias a la versión ${old} en: ${files[*]}"
  fi
}

# update_version_refs: refreshes the version references found in README.md and
# in the docs (docs/MANUAL*.md, man pages) — step 5.
update_version_refs() {
  local old="${LAST_TAG#v}"
  if [[ "${old}" == "${NEW_VERSION}" ]]; then
    return 0
  fi
  local -a targets=()
  local f
  while IFS= read -r f; do
    targets+=("${f}")
  done < <(version_files_with_ref "${old}")

  if [[ "${#targets[@]}" -eq 0 ]]; then
    info "No hay referencias a la versión ${old} en README.md ni en docs/."
    return 0
  fi
  info "Referencias a la versión ${old} en: ${targets[*]}"
  if confirm "¿Actualizar esos archivos (${old} → ${NEW_VERSION})?"; then
    # Replace v<old> (URLs/tags) first, then any remaining bare <old>
    # (badge, .deb, man page header). No \b: it would not match inside
    # "v0.1.0" or "onecloudriver_0.1.0_amd64.deb".
    sed -i "s/v${old}/${NEW_TAG}/g; s/${old}/${NEW_VERSION}/g" "${targets[@]}"
    ok "Actualizados: ${targets[*]}"
  fi
}

# -----------------------------------------------------------------------------
# Commit + tag + push
# -----------------------------------------------------------------------------
commit_and_tag() {
  local -a files=("${CHANGELOG}")
  local f
  # Include any versioned files (README.md + docs) that were modified.
  for f in README.md ${DOC_VERSION_PATTERNS[*]}; do
    [[ -f "${f}" ]] || continue
    [[ -n "$(git status --porcelain "${f}" 2>/dev/null)" ]] && files+=("${f}")
  done
  git add "${files[@]}"
  if [[ -z "$(git diff --cached --name-only)" ]]; then
    warn "No hay cambios que commitear; se creará el tag directamente."
  else
    git commit -q -m "chore(release): prepare release ${NEW_TAG}"
    ok "Commit creado: chore(release): prepare release ${NEW_TAG}"
  fi
  git tag -a "${NEW_TAG}" -m "Release ${NEW_TAG}"
  ok "Tag creado: ${NEW_TAG}"
}

push_and_monitor() {
  local run_id=""
  if [[ -n "${CURRENT_BRANCH}" ]]; then
    git push "${REMOTE}" "${CURRENT_BRANCH}"
    ok "Push de la rama '${CURRENT_BRANCH}' completado."
  fi
  git push "${REMOTE}" "${NEW_TAG}"
  ok "Push del tag ${NEW_TAG} completado — el workflow Release se ha disparado."

  info "Esperando a que GitHub registre el run del workflow..."
  local i
  for i in $(seq 1 15); do
    run_id="$(gh run list --workflow "${RELEASE_WORKFLOW}" --event push --limit 10 \
      --json databaseId,headBranch \
      --jq ".[] | select(.headBranch==\"${NEW_TAG}\") | .databaseId" 2>/dev/null | head -1 || true)"
    [[ -n "${run_id}" ]] && break
    sleep 4
  done

  if [[ -z "${run_id}" ]]; then
    warn "No se encontró el run del workflow. Revisa manualmente:"
    warn "  gh run list --workflow ${RELEASE_WORKFLOW}"
    warn "  https://github.com/${REPO}/actions"
    return 0
  fi

  info "Workflow run: https://github.com/${REPO}/actions/runs/${run_id}"
  if confirm "¿Ver el progreso del run en vivo (gh run watch)?"; then
    gh run watch "${run_id}" || true
  fi

  local conclusion
  conclusion="$(gh run view "${run_id}" --json conclusion --jq .conclusion 2>/dev/null || true)"
  if [[ "${conclusion}" == "success" ]]; then
    ok "Workflow completado con éxito."
    gh release view "${NEW_TAG}" --json name,url \
      --jq '"Release \(.name) publicada: \(.url)"' 2>/dev/null \
      || warn "El workflow terminó bien, pero la release aún no aparece. Reintenta en unos segundos."
  elif [[ -z "${conclusion}" || "${conclusion}" == "null" ]]; then
    info "El workflow sigue en progreso. Consulta el estado en:"
    info "  https://github.com/${REPO}/actions/runs/${run_id}"
  else
    warn "El workflow terminó en: ${conclusion}. Revisa el run para ver el error:"
    warn "  https://github.com/${REPO}/actions/runs/${run_id}"
  fi
}

# -----------------------------------------------------------------------------
# Main flows
# -----------------------------------------------------------------------------
print_checklist_header() {
  hr
  printf '%b\n' "${C_BOLD}  RELEASE CHECKLIST — ${REPO}${C_RESET}"
  hr
}

preflight() {
  check_gh
  detect_repo
  detect_default_branch
  print_checklist_header
  check_git_state
  check_prs
  check_ci
  get_last_tag
  check_version_refs
  info "Próxima versión propuesta: v$(propose_version "${LAST_TAG}")"
  hr
  if [[ "${CHECKS_FAILED}" -ne 0 ]]; then
    die "Hay ${CHECKS_FAILED} problema(s) crítico(s) que resolver antes de publicar."
  fi
}

run_check() {
  preflight
  ok "Checklist completado (solo lectura). Nada ha sido modificado."
}

run_full() {
  preflight

  # ── 0. Merge de PRs (opcional) ───────────────────────────────────────────
  merge_prs

  # ── 1. Selección de versión ──────────────────────────────────────────────
  local proposed
  proposed="$(propose_version "${LAST_TAG}")"
  local input=""
  read -r -p "Versión a publicar (último tag ${LAST_TAG}) [${proposed}]: " input || true
  NEW_VERSION="${input:-${proposed}}"
  NEW_VERSION="${NEW_VERSION#v}"
  while ! valid_version "${NEW_VERSION}"; do
    err "Formato inválido (esperado X.Y.Z, p.ej. 0.1.1 o 0.2.0-rc.1)."
    read -r -p "Versión a publicar [${proposed}]: " input || true
    NEW_VERSION="${input:-${proposed}}"
    NEW_VERSION="${NEW_VERSION#v}"
  done
  NEW_TAG="v${NEW_VERSION}"
  hr
  printf '%b\n' "${C_BOLD}  Publicando ${C_RESET}${C_GREEN}${C_BOLD}${NEW_TAG}${C_RESET}"
  hr
  check_version_availability "${NEW_VERSION}"

  # ── 2. CHANGELOG ─────────────────────────────────────────────────────────
  info "Insertando sección [${NEW_VERSION}] en CHANGELOG.md (borrador desde commits)..."
  insert_changelog_section
  info "Abriendo tu editor (${EDITOR:-vi}) para revisar el borrador..."
  launch_editor
  if ! grep -qE "^## \[${NEW_VERSION}\]" "${CHANGELOG}"; then
    die "No se encontró la sección [${NEW_VERSION}] en CHANGELOG.md tras editar."
  fi
  git diff --stat "${CHANGELOG}"
  if ! confirm "¿Es correcto el CHANGELOG?"; then
    info "Ábrelo de nuevo para corregirlo (o Ctrl-C para abortar)..."
    launch_editor
  fi

  # ── 3. README + docs (opcional) ──────────────────────────────────────────
  update_version_refs

  # ── 4. Commit + tag + push ───────────────────────────────────────────────
  if ! confirm "¿Commitear, crear el tag ${NEW_TAG} y hacer push? (se disparará el workflow Release)"; then
    die "Abortado por el usuario. CHANGELOG.md, README.md y docs/ pueden haber quedado modificados; revísalos con 'git diff' o revierte con 'git checkout'."
  fi
  commit_and_tag
  push_and_monitor

  hr
  ok "Release ${NEW_TAG} en marcha."
  info "Verifica que los artefactos (zip, .deb, .rpm) aparezcan en:"
  info "  https://github.com/${REPO}/releases/tag/${NEW_TAG}"
  hr
}

main() {
  local arg
  for arg in "$@"; do
    case "${arg}" in
      --check) MODE="check" ;;
      --yes)   ASSUME_YES=1 ;;
      --help|-h) usage; exit 0 ;;
      *) die "Argumento desconocido: ${arg} (usa --help)." ;;
    esac
  done

  if [[ "${MODE}" == "check" ]]; then
    run_check
  else
    run_full
  fi
}

# Only run main when executed directly (allows `source` for testing helpers).
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
