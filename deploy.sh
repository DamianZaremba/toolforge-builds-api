#!/bin/bash
# local deployment only
set -o errexit
set -o nounset
set -o pipefail

# explicitly find and specify path to helmfile to allow invoking
# this script without having to cd to the deployment directory
BASE_DIR=$(dirname "$(realpath -s "$0")")
cd "$BASE_DIR"

# use -i (interactive) to ask for confirmation for changing
# live cluster state if stdin is a tty
if [[ -t 0 ]]; then
	INTERACTIVE_PARAM="-i"
else
	INTERACTIVE_PARAM=""
fi

# helmfile apply will show a diff before doing changes
helmfile -e local --file "./deployment/helmfile.yaml.gotmpl" $INTERACTIVE_PARAM apply
