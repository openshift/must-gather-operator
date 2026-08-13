#!/usr/bin/env bash
#
# E2E coverage lifecycle script for CI and local use.
#
# Usage:
#   hack/e2e-coverage.sh setup    Prepare the operator for coverage collection
#   hack/e2e-coverage.sh collect  Collect, convert, and optionally upload coverage data
#
# Environment variables:
#   COVERAGE_IMAGE  (setup)   Full pullspec of the coverage-instrumented image
#   CODECOV_TOKEN   (collect) Codecov upload token; skip upload if unset
#   ARTIFACT_DIR    (collect) Directory for CI artifacts; defaults to "."
set -euo pipefail

NAMESPACE="must-gather-operator"
DEPLOYMENT="must-gather-operator"
CONTAINER="must-gather-operator"
GOCOVERDIR_PATH="/tmp/e2e-cover"
CODECOV_SECRET_PATH="/var/run/secrets/must-gather-operator/ci-secrets/CODECOV_TOKEN"
POD_LABEL="name=must-gather-operator"

# JSON Patch helper: append to an array if it exists, otherwise create it.
# Using ".../field/-" fails when the parent array is absent.
coverage_volume_ops() {
	local volume_mounts_path="$1"
	local volumes_path="$2"
	local has_volume_mounts="$3"
	local has_volumes="$4"

	local volume_mounts_op volumes_op
	if [[ -n "${has_volume_mounts}" ]]; then
		volume_mounts_op="{\"op\": \"add\", \"path\": \"${volume_mounts_path}/-\", \"value\": {\"name\": \"coverage-data\", \"mountPath\": \"${GOCOVERDIR_PATH}\"}}"
	else
		volume_mounts_op="{\"op\": \"add\", \"path\": \"${volume_mounts_path}\", \"value\": [{\"name\": \"coverage-data\", \"mountPath\": \"${GOCOVERDIR_PATH}\"}]}"
	fi
	if [[ -n "${has_volumes}" ]]; then
		volumes_op="{\"op\": \"add\", \"path\": \"${volumes_path}/-\", \"value\": {\"name\": \"coverage-data\", \"emptyDir\": {}}}"
	else
		volumes_op="{\"op\": \"add\", \"path\": \"${volumes_path}\", \"value\": [{\"name\": \"coverage-data\", \"emptyDir\": {}}]}"
	fi

	printf '%s\n%s\n' "${volume_mounts_op}" "${volumes_op}"
}

# Find env var index by name in a CSV container env list (empty if absent).
csv_env_index() {
	local csv="$1"
	local env_name="$2"
	local i=0
	local name
	while IFS= read -r name; do
		if [[ "${name}" == "${env_name}" ]]; then
			echo "${i}"
			return 0
		fi
		i=$((i + 1))
	done < <(oc get csv "${csv}" -n "${NAMESPACE}" \
		-o jsonpath='{range .spec.install.spec.deployments[0].spec.template.spec.containers[0].env[*]}{.name}{"\n"}{end}' 2>/dev/null)
	return 1
}

setup() {
	echo "--- E2E Coverage Setup ---"

	if [[ -z "${COVERAGE_IMAGE:-}" ]]; then
		echo "Error: COVERAGE_IMAGE env var must be set"
		exit 1
	fi
	echo "Coverage image: ${COVERAGE_IMAGE}"

	local csv
	csv=$(oc get deployment "${DEPLOYMENT}" -n "${NAMESPACE}" \
		-o jsonpath='{.metadata.ownerReferences[?(@.kind=="ClusterServiceVersion")].name}' 2>/dev/null)

	if [[ -n "${csv}" ]]; then
		echo "Found CSV: ${csv} -- patching via CSV"
		# Assumes deployments/0 and containers/0 are the must-gather-operator.
		# Inject coverage-data emptyDir at GOCOVERDIR so data survives container
		# restart. Create volumeMounts/volumes arrays when absent (JSON Patch
		# ".../-" is invalid if the parent array does not exist).
		local pod_spec_path="/spec/install/spec/deployments/0/spec/template/spec"
		local container_path="${pod_spec_path}/containers/0"

		local has_volume_mounts has_volumes has_coverage_vol
		has_volume_mounts=$(oc get csv "${csv}" -n "${NAMESPACE}" \
			-o jsonpath="{.spec.install.spec.deployments[0].spec.template.spec.containers[0].volumeMounts}" 2>/dev/null || true)
		has_volumes=$(oc get csv "${csv}" -n "${NAMESPACE}" \
			-o jsonpath="{.spec.install.spec.deployments[0].spec.template.spec.volumes}" 2>/dev/null || true)
		has_coverage_vol=$(oc get csv "${csv}" -n "${NAMESPACE}" \
			-o jsonpath='{.spec.install.spec.deployments[0].spec.template.spec.volumes[?(@.name=="coverage-data")].name}' 2>/dev/null || true)

		local operator_image_idx=""
		operator_image_idx=$(csv_env_index "${csv}" "OPERATOR_IMAGE" || true)

		local operator_image_op
		if [[ -n "${operator_image_idx}" ]]; then
			operator_image_op="{\"op\": \"replace\", \"path\": \"${container_path}/env/${operator_image_idx}/value\", \"value\": \"${COVERAGE_IMAGE}\"}"
		else
			operator_image_op="{\"op\": \"add\", \"path\": \"${container_path}/env/-\", \"value\": {\"name\": \"OPERATOR_IMAGE\", \"value\": \"${COVERAGE_IMAGE}\"}}"
		fi

		local has_gocoverdir
		has_gocoverdir=$(oc get csv "${csv}" -n "${NAMESPACE}" \
			-o jsonpath='{.spec.install.spec.deployments[0].spec.template.spec.containers[0].env[?(@.name=="GOCOVERDIR")].name}' 2>/dev/null || true)

		local -a patch_ops=(
			"{\"op\": \"replace\", \"path\": \"${container_path}/image\", \"value\": \"${COVERAGE_IMAGE}\"}"
			"${operator_image_op}"
		)
		if [[ -z "${has_gocoverdir}" ]]; then
			patch_ops+=("{\"op\": \"add\", \"path\": \"${container_path}/env/-\", \"value\": {\"name\": \"GOCOVERDIR\", \"value\": \"${GOCOVERDIR_PATH}\"}}")
		else
			echo "GOCOVERDIR env var already present in CSV"
		fi

		if [[ -z "${has_coverage_vol}" ]]; then
			local volume_mounts_op volumes_op
			{
				read -r volume_mounts_op
				read -r volumes_op
			} < <(coverage_volume_ops "${container_path}/volumeMounts" "${pod_spec_path}/volumes" "${has_volume_mounts}" "${has_volumes}")
			patch_ops+=("${volume_mounts_op}" "${volumes_op}")
		else
			echo "Volume 'coverage-data' already present in CSV -- skipping volume patch"
		fi

		local patch_json
		patch_json=$(printf '%s\n' "${patch_ops[@]}" | paste -sd ',' -)
		oc patch csv "${csv}" -n "${NAMESPACE}" --type=json -p "[${patch_json}]"
	else
		echo "No CSV found -- patching deployment directly"
		oc set image "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" \
			"${CONTAINER}=${COVERAGE_IMAGE}"
		oc set env "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" \
			-c "${CONTAINER}" GOCOVERDIR="${GOCOVERDIR_PATH}" OPERATOR_IMAGE="${COVERAGE_IMAGE}"

		local has_vol has_volume_mounts has_volumes
		has_vol=$(oc get "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" \
			-o jsonpath='{.spec.template.spec.volumes[?(@.name=="coverage-data")].name}' 2>/dev/null || true)
		if [[ -z "${has_vol}" ]]; then
			has_volume_mounts=$(oc get "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" \
				-o jsonpath='{.spec.template.spec.containers[0].volumeMounts}' 2>/dev/null || true)
			has_volumes=$(oc get "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" \
				-o jsonpath='{.spec.template.spec.volumes}' 2>/dev/null || true)

			local volume_mounts_op volumes_op
			{
				read -r volume_mounts_op
				read -r volumes_op
			} < <(coverage_volume_ops "/spec/template/spec/containers/0/volumeMounts" "/spec/template/spec/volumes" "${has_volume_mounts}" "${has_volumes}")

			oc patch "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" --type=json -p "[
				${volume_mounts_op},
				${volumes_op}
			]"
		else
			echo "Volume 'coverage-data' already exists -- skipping volume patch"
		fi
	fi

	echo "Waiting for operator rollout with coverage image..."
	oc rollout status "deployment/${DEPLOYMENT}" -n "${NAMESPACE}" --timeout=180s

	echo "Verifying GOCOVERDIR is set in the running pod..."
	oc exec -n "${NAMESPACE}" "deploy/${DEPLOYMENT}" -- env | grep GOCOVERDIR || \
		echo "Warning: GOCOVERDIR not found in pod env (non-fatal)"

	echo "--- Coverage setup complete ---"
}

collect() {
	echo "--- E2E Coverage Collection ---"

	local artifact_dir="${ARTIFACT_DIR:-.}"
	local coverage_dir="${artifact_dir}/e2e-cover-data"
	local coverage_profile="${artifact_dir}/coverage-e2e.out"

	if [[ -z "${CODECOV_TOKEN:-}" ]] && [[ -f "${CODECOV_SECRET_PATH}" ]]; then
		CODECOV_TOKEN=$(cat "${CODECOV_SECRET_PATH}")
		export CODECOV_TOKEN
	fi

	local pod
	pod=$(oc get pod -n "${NAMESPACE}" -l "${POD_LABEL}" \
		--field-selector=status.phase=Running \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
	if [[ -z "${pod}" ]]; then
		echo "Error: no operator pod found in namespace ${NAMESPACE}"
		exit 1
	fi
	echo "Operator pod: ${pod}"

	echo "Sending SIGTERM to operator process to flush coverage data..."
	oc exec -n "${NAMESPACE}" "${pod}" -c "${CONTAINER}" -- /bin/sh -c 'kill -TERM 1' || true

	echo "Waiting for container to restart..."
	oc wait pod/"${pod}" --for=condition=Ready=False -n "${NAMESPACE}" --timeout=30s 2>/dev/null || true
	oc wait pod/"${pod}" --for=condition=Ready -n "${NAMESPACE}" --timeout=120s

	mkdir -p "${coverage_dir}"
	echo "Copying coverage data from operator pod..."
	oc cp "${NAMESPACE}/${pod}:${GOCOVERDIR_PATH}/." "${coverage_dir}" -c "${CONTAINER}"

	echo "Coverage files:"
	ls -la "${coverage_dir}/" 2>/dev/null || true

	if ls "${coverage_dir}"/covmeta.* >/dev/null 2>&1; then
		echo "Converting coverage data to Go profile format..."
		go tool covdata textfmt -i="${coverage_dir}" -o="${coverage_profile}"

		echo ""
		echo "=== E2E Coverage Summary ==="
		go tool covdata percent -i="${coverage_dir}"
		echo "============================="
		echo ""
		echo "Coverage profile: ${coverage_profile} ($(wc -l < "${coverage_profile}") lines)"

		if [[ -n "${CODECOV_TOKEN:-}" ]]; then
			echo "Uploading to Codecov..."
			local codecov_version="v0.8.0"
			local codecov_bin="${artifact_dir}/codecov"
			local codecov_os codecov_asset
			codecov_os="$(uname -s)"
			case "${codecov_os}" in
				Linux)
					codecov_asset="linux/codecov"
					;;
				Darwin)
					# Official macos asset supports Intel and Apple Silicon.
					codecov_asset="macos/codecov"
					;;
				*)
					echo "Error: unsupported OS for Codecov uploader: ${codecov_os}"
					exit 1
					;;
			esac
			echo "Downloading Codecov uploader for ${codecov_asset}"
			curl -sS -o "${codecov_bin}" \
				"https://uploader.codecov.io/${codecov_version}/${codecov_asset}"
			curl -sS -o "${codecov_bin}.SHA256SUM" \
				"https://uploader.codecov.io/${codecov_version}/${codecov_asset}.SHA256SUM"

			# macOS ships shasum; Linux ships sha256sum.
			if !(
				cd "$(dirname "${codecov_bin}")"
				if command -v sha256sum >/dev/null 2>&1; then
					sha256sum -c "$(basename "${codecov_bin}").SHA256SUM"
				else
					shasum -a 256 -c "$(basename "${codecov_bin}").SHA256SUM"
				fi
			); then
				echo "Error: Codecov binary checksum verification failed"
				exit 1
			fi
			chmod +x "${codecov_bin}"

			local -a codecov_args=(
				--file="${coverage_profile}"
				--flags=e2e
				--name="E2E Coverage"
				--verbose
			)

			local job_type="${JOB_TYPE:-local}"
			if [[ "${job_type}" == "presubmit" ]]; then
				echo "Detected presubmit (PR #${PULL_NUMBER:-unknown})"
				[[ -n "${PULL_NUMBER:-}" ]] && codecov_args+=(--pr "${PULL_NUMBER}")
				[[ -n "${PULL_PULL_SHA:-}" ]] && codecov_args+=(--sha "${PULL_PULL_SHA}")
				[[ -n "${PULL_BASE_REF:-}" ]] && codecov_args+=(--branch "${PULL_BASE_REF}")
				[[ -n "${REPO_OWNER:-}" && -n "${REPO_NAME:-}" ]] && codecov_args+=(--slug "${REPO_OWNER}/${REPO_NAME}")
			elif [[ "${job_type}" == "postsubmit" ]]; then
				echo "Detected postsubmit (branch ${PULL_BASE_REF:-unknown})"
				[[ -n "${PULL_BASE_SHA:-}" ]] && codecov_args+=(--sha "${PULL_BASE_SHA}")
				[[ -n "${PULL_BASE_REF:-}" ]] && codecov_args+=(--branch "${PULL_BASE_REF}")
				[[ -n "${REPO_OWNER:-}" && -n "${REPO_NAME:-}" ]] && codecov_args+=(--slug "${REPO_OWNER}/${REPO_NAME}")
			else
				echo "Local run -- no Prow context, Codecov will auto-detect from git"
			fi

			"${codecov_bin}" "${codecov_args[@]}" || echo "Warning: Codecov upload failed (non-fatal)"
			rm -f "${codecov_bin}" "${codecov_bin}.SHA256SUM"
		else
			echo "CODECOV_TOKEN not set -- skipping Codecov upload."
			echo "Coverage profile saved as artifact: ${coverage_profile}"
		fi
	else
		echo "Warning: No coverage data found in ${coverage_dir}"
		echo "The operator may not have been built with coverage instrumentation,"
		echo "or it may not have exited cleanly (SIGKILL instead of SIGTERM)."
	fi

	echo "--- Coverage collection complete ---"
}

case "${1:-}" in
	setup)
		setup
		;;
	collect)
		collect
		;;
	*)
		echo "Usage: $0 {setup|collect}" >&2
		exit 1
		;;
esac
