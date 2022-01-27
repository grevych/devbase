#!/usr/bin/env bash
# Get bootstrap information

REPODIR=""
DEVBASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." >/dev/null 2>&1 && pwd)"

find_service_yaml() {
  local path="$1"

  # We may want to do something better here one day
  if [[ $path == "/" ]]; then
    echo "Error: Failed to find service.yaml"
    exit 1
  fi

  if [[ -e "$path/service.yaml" ]]; then
    REPODIR="$path"
    return
  fi

  # traverse back a dir
  find_service_yaml "$(cd "$path/.." >/dev/null 2>&1 && pwd)"
}

find_repo_dir() {
  find_service_yaml "$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
}
find_repo_dir

get_repo_directory() {
  echo "$REPODIR"
}

get_app_name() {
  yq -r '.name' <"$(get_service_yaml)"
}

# get_tool_version reads a version from .bootstrap/versions.yaml
# and returns it
get_tool_version() {
  name="$1"
  yq -r ".[\"$name\"]" <"$DEVBASE_DIR/versions.yaml"
}

# get_application_version executes get_tool_version
# Deprecated: use get_tool_version instead
get_application_version() {
  get_tool_version "$1"
}

# has_feature returns 0 if a value is true
# or 1 if false
has_feature() {
  local feat="$1"

  val=$(yq -r ".[\"$feat\"]" <"$(get_service_yaml)")

  if [[ $val == "true" ]]; then
    return 0
  fi

  return 1
}

get_service_yaml() {
  if [[ -e "service.yaml" ]]; then
    echo "service.yaml"
  else
    echo "$REPODIR/service.yaml"
  fi
}

has_resource() {
  local name="$1"

  # Check if the resource is present
  if [[ -n "$(get_resource_version "$name")" ]]; then
    return 0
  fi

  return 1
}

get_resource_version() {
  local name="$1"

  if [[ "$(yq -r ".resources[\"$name\"]" <"$(get_service_yaml)")" == "null" ]]; then
    echo ""
  else
    yq -r ".resources[\"$name\"]" <"$(get_service_yaml)"
  fi
}

has_grpc_client() {
  local name="$1"

  if [[ "$(yq -r '.grpcClients' <"$(get_service_yaml)")" == "null" ]]; then
    return 1
  fi

  if [[ -n "$(yq -r ".grpcClients[] | select(. == \"$name\")" <"$(get_service_yaml)")" ]]; then
    return 0
  fi

  return 1
}

get_list() {
  local name="$1"

  if [[ "$(yq -rc ".\"$name\"" <"$(get_service_yaml)")" == "null" ]]; then
    echo ""
  else
    yq -rc ".\"$name\"[]" <"$(get_service_yaml)"
  fi
}

get_keys() {
  local name="$1"

  if [[ "$(yq -r ".\"$name\"" <"$(get_service_yaml)")" == "null" ]]; then
    echo ""
  else
    yq -r ".\"$name\" | keys[]" <"$(get_service_yaml)"
  fi
}
