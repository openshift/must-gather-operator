#!/usr/bin/env bash
#
# Require the matching docs/design/ file when architectural sources are staged.
# Used by the pre-commit hook check-design-docs.
#
# Skip: SKIP=check-design-docs git commit ...
set -euo pipefail

if [[ "${SKIP:-}" == *check-design-docs* ]]; then
	exit 0
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	exit 0
fi

staged="$(git diff --cached --name-only --diff-filter=ACMR || true)"
if [[ -z "${staged}" ]]; then
	exit 0
fi

design_doc_for() {
	case "$1" in
	api/*/mustgather_types.go)
		echo "docs/design/api.md"
		;;
	controllers/mustgather/mustgather_controller.go)
		echo "docs/design/controller.md"
		;;
	controllers/mustgather/template.go)
		echo "docs/design/job-template.md"
		;;
	controllers/mustgather/predicates.go)
		echo "docs/design/predicates.md"
		;;
	controllers/mustgather/validation.go | build/bin/*)
		echo "docs/design/upload.md"
		;;
	esac
}

missing=0
while IFS= read -r f; do
	[[ -z "${f}" ]] && continue
	doc="$(design_doc_for "${f}" || true)"
	[[ -z "${doc}" ]] && continue
	if ! grep -Fxq "${doc}" <<<"${staged}"; then
		echo "Changed ${f} without staging ${doc}." >&2
		missing=1
	fi
done <<<"${staged}"

if [[ "${missing}" -ne 0 ]]; then
	echo "Review and update preconditions, invariants, and rationale in the matching design doc." >&2
	echo "Map: docs/design/README.md" >&2
	echo "Skip (not for architectural changes): SKIP=check-design-docs git commit ..." >&2
	exit 1
fi
