#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage:
#   ./hack/update-metadata.sh [OCP_VERSION]
#
#   OCP_VERSION is an optional argument. If no argument is provided, it defaults
#   to the version found in .channels[0].currentCSV in PACKAGE_MANIFEST.
#   This means you can run `./hack/update-metadata.sh` to update the manifests
#   using the current package version, or you can for example run
#   `./hack/update-metadata.sh 4.21` to set the package version to 4.21.
#   Both PACKAGE_MANIFEST and CSV_MANIFEST will be updated by this script.

PACKAGE_MANIFEST=bundle/manifests/support-log-gather-operator.package.yaml
CHANNEL=$(yq '.channels[0].name' ${PACKAGE_MANIFEST})
CURRENT_CSV=$(yq '.channels[0].currentCSV' ${PACKAGE_MANIFEST})
PACKAGE_NAME=$(echo ${CURRENT_CSV} | sed 's/\.v.*$//')
PACKAGE_VERSION=$(echo ${CURRENT_CSV} | sed 's/^.*\.v//')

if [ -z "${CHANNEL}" ] ||
   [ -z "${PACKAGE_NAME}" ] ||
   [ -z "${PACKAGE_VERSION}" ]; then
    echo "Failed to parse ${PACKAGE_MANIFEST}"
    exit 1
fi

CSV_MANIFEST=bundle/manifests/${CHANNEL}/${PACKAGE_NAME}.clusterserviceversion.yaml
METADATA_NAME=$(yq ' "" + .metadata.name' ${CSV_MANIFEST})
SKIP_RANGE=$(yq ' "" + .metadata.annotations["olm.skipRange"]' ${CSV_MANIFEST})
SPEC_VERSION=$(yq ' "" + .spec.version' ${CSV_MANIFEST})

# olm.properties may not exist yet, so use default if missing
OLM_PROPERTIES=$(yq ' "" + .metadata.annotations["olm.properties"]' ${CSV_MANIFEST})
if [ "${OLM_PROPERTIES}" == "null" ] || [ -z "${OLM_PROPERTIES}" ]; then
    echo "Warning: olm.properties not found in CSV, will be created with default value"
    OLM_PROPERTIES='[{"type":"olm.maxOpenShiftVersion","value":"4.21"}]'
fi

if [ -z "${METADATA_NAME}" ] ||
   [ -z "${SKIP_RANGE}" ] ||
   [ -z "${SPEC_VERSION}" ]; then
    echo "Failed to parse ${CSV_MANIFEST}"
    exit 1
fi

OCP_VERSION=${1:-${PACKAGE_VERSION}}
IFS='.' read -r MAJOR_VERSION MINOR_VERSION PATCH_VERSION <<< "${OCP_VERSION}"
PATCH_VERSION=${PATCH_VERSION:-0}
if [ "${OCP_VERSION}" != "${PACKAGE_VERSION}" ]; then
    PACKAGE_VERSION="${MAJOR_VERSION}.${MINOR_VERSION}.${PATCH_VERSION}"
fi

export NEW_CURRENT_CSV="${PACKAGE_NAME}.v${PACKAGE_VERSION}"
export NEW_METADATA_NAME="${PACKAGE_NAME}.v${PACKAGE_VERSION}"
export NEW_SKIP_RANGE=$(echo ${SKIP_RANGE} | sed "s/ <.*$/ <${PACKAGE_VERSION}/")
export NEW_OLM_PROPERTIES=$(echo "${OLM_PROPERTIES}" | jq -c 'map(if .type=="olm.maxOpenShiftVersion" then .value="'${MAJOR_VERSION}.$((MINOR_VERSION + 1))'" else . end)')
export NEW_SPEC_VERSION="${PACKAGE_VERSION}"

if [ -z "${NEW_METADATA_NAME}" ] ||
   [ -z "${NEW_SKIP_RANGE}" ] ||
   [ -z "${NEW_OLM_PROPERTIES}" ] ||
   [ -z "${NEW_SPEC_VERSION}" ]; then
    echo "Failed to generate new values for ${CSV_MANIFEST}"
    exit 1
fi

echo "Updating package manifest to ${PACKAGE_VERSION}"
yq -i '.channels[0].currentCSV = strenv(NEW_CURRENT_CSV)' ${PACKAGE_MANIFEST}

echo "Updating OLM metadata to ${PACKAGE_VERSION}"
yq -i '
  .metadata.name = strenv(NEW_METADATA_NAME) |
  .metadata.annotations["olm.skipRange"] = strenv(NEW_SKIP_RANGE) |
  .metadata.annotations["olm.properties"] = strenv(NEW_OLM_PROPERTIES) |
  .spec.version = strenv(NEW_SPEC_VERSION)
' ${CSV_MANIFEST}

echo ""
echo "Successfully updated OLM metadata to version ${PACKAGE_VERSION}"
echo "  - Package CSV: ${NEW_CURRENT_CSV}"
echo "  - Skip Range: ${NEW_SKIP_RANGE}"
echo "  - Max OpenShift Version: ${MAJOR_VERSION}.$((MINOR_VERSION + 1))"
echo ""
echo "Updated files:"
echo "  - ${PACKAGE_MANIFEST}"
echo "  - ${CSV_MANIFEST}"
