#!/usr/bin/env bash
set -x

for j in $(kubectl get vm -n "${1}" | grep prairie | awk '{print $1}')
do
    kubectl delete vm "${j}"
done

for i in $(kubectl get pvc -n "${1}" | grep prairie | awk '{print $1}')
do
    kubectl delete pvc -n "${1}" "${i}"
done
