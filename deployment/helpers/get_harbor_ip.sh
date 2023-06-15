#!/bin/bash

set -o nounset
set -o errexit
set -o pipefail

if [[ "${HARBOR_IP:-}" == "" ]]; then
    if [[ -f /etc/debian_version ]]; then
        guessed_harbor_ip=$(hostname -I| awk '{print $1}')
    else
        if hash minikube 2>/dev/null; then
            guessed_harbor_ip=$(minikube ssh "grep host.minikube.internal /etc/hosts" | awk '{print $1}')
        else
            echo "Unable to guess harbor ip, please set one by exporting the env variable HARBOR_IP" >&2
            exit 1
        fi
    fi
    echo -n "$guessed_harbor_ip"
else
    echo  -n "$HARBOR_IP"
fi
