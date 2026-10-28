#!/usr/bin/env bash
# Registry metadata fixtures shared by the existing update/CLI regressions.
mock_image_role() {
    case "$1" in
        *qcontrol-plane*) printf CONTROL ;;
        *qcontrol-web*) printf WEB ;;
        *postgres*) printf POSTGRES ;;
        *) return 1 ;;
    esac
}

mock_target_value() {
    local role key default
    role="$(mock_image_role "$1")"
    key="${QCH_IMAGE_TEST_PREFIX}_TARGET_${role}_$2"
    case "$role:$2" in
        CONTROL:ID) default="sha256:$(printf '%064d' 1)" ;;
        WEB:ID) default="sha256:$(printf '%064d' 2)" ;;
        POSTGRES:ID) default="sha256:$(printf '%064d' 3)" ;;
        CONTROL:VERSION) default=new-control-version ;;
        WEB:VERSION) default=new-web-version ;;
        POSTGRES:VERSION) default=old-postgres-version ;;
    esac
    printf '%s' "${!key:-$default}"
}

mock_image_manifest() {
    printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"%s"},"layers":[]}' "$(mock_target_value "$1" ID)"
}

mock_pinned_ref() {
    local repository="${1%%@*}"
    if [[ "${repository##*/}" == *:* ]]; then repository="${repository%:*}"; fi
    printf '%s@sha256:%s' "$repository" "$(mock_image_manifest "$1" | openssl dgst -sha256 | awk '{print $NF}')"
}

mock_local_image() {
    local ref="$1" id platform arch variant
    id="$(mock_target_value "$ref" ID)"
    case "${QCH_MOCK_LOCAL_STORE:-classic}" in
        containerd) id="${ref##*@}" ;;
        mismatch) id="sha256:$(printf '%064d' 999)" ;;
        unavailable) return 1 ;;
    esac
    platform="${QCH_MOCK_LOCAL_PLATFORM:-${QCH_MOCK_IMAGE_PLATFORM:-linux/amd64}}"
    arch="${platform#*/}"; variant="${arch#*/}"; arch="${arch%%/*}"
    [ "$variant" != "$arch" ] || variant=""
    jq -cn --arg id "$id" --arg os "${platform%%/*}" --arg arch "$arch" --arg variant "$variant" \
        '{Id:$id,Os:$os,Architecture:$arch,Variant:$variant}'
}

mock_current_image() {
    local id="$1" ref="$2"
    if [ "${QCH_MOCK_CURRENT_STORE:-classic}" = containerd ]; then
        ref="$(mock_pinned_ref "$ref")"
        jq -cn --arg id "$id" --arg digest "${ref##*@}" '{Image:$id,ImageManifestDescriptor:{digest:$digest}}'
    else
        jq -cn --arg id "$id" '{Image:$id}'
    fi
}

mock_registry() {
    local ref="${*: -1}" key platform arch variant digest
    key="${QCH_IMAGE_TEST_PREFIX}_FAIL_REMOTE"
    [ "${!key:-false}" = false ] || return 1
    case " $* " in
        *" --raw "*)
            case "${QCH_MOCK_REGISTRY_MODE:-single}:$ref" in
                bad-json:*) printf '{' ;;
                missing-id:*) printf '{"schemaVersion":2}' ;;
                index:*@*|ambiguous:*@*|missing-platform:*@*|wrong-digest:*@*) mock_image_manifest "$ref" ;;
                index:*|ambiguous:*|missing-platform:*|wrong-digest:*)
                    digest="$(mock_pinned_ref "$ref")"; digest="${digest##*@}"
                    [ "${QCH_MOCK_REGISTRY_MODE}" != wrong-digest ] || digest="sha256:$(printf '%064d' 99)"
                    jq -cn --arg digest "$digest" --arg mode "$QCH_MOCK_REGISTRY_MODE" '{schemaVersion:2,manifests:[
                        {digest:$digest,platform:{os:"linux",architecture:"amd64"}},
                        {digest:("sha256:"+"a"*64),platform:{os:"linux",architecture:"arm64",variant:"v8"}},
                        {digest:("sha256:"+"b"*64),platform:{os:"unknown",architecture:"unknown"}}
                    ]} | if $mode == "ambiguous" then .manifests += [.manifests[0]]
                         elif $mode == "missing-platform" then .manifests = [.manifests[1]] else . end'
                    ;;
                *) mock_image_manifest "$ref" ;;
            esac
            ;;
        *" --format "*)
            [ "${QCH_MOCK_REGISTRY_MODE:-}" != metadata-failure ] || return 1
            platform="${QCH_MOCK_IMAGE_PLATFORM:-linux/amd64}"
            arch="${platform#*/}"; variant="${arch#*/}"; arch="${arch%%/*}"
            [ "$variant" != "$arch" ] || variant=""
            jq -cn --arg os "${platform%%/*}" --arg arch "$arch" --arg variant "$variant" \
                --arg version "$(mock_target_value "$ref" VERSION)" \
                '{os:$os,architecture:$arch,variant:$variant,config:{Labels:{"org.opencontainers.image.version":$version}}}'
            ;;
        *) return 1 ;;
    esac
}

mock_assert_comparison() {
    [ -z "${QCH_MOCK_OUTPUT:-}" ] || grep -Fq '镜像对比结果' "$QCH_MOCK_OUTPUT" || return 1
}

mock_compose_images() {
    local tag="${1:-latest}"
    jq -cn --arg tag "$tag" --arg platform "${QCH_MOCK_COMPOSE_PLATFORM:-}" \
        --arg default "${DOCKER_DEFAULT_PLATFORM:-${QCH_MOCK_ENV_PLATFORM:-}}" '{"x-qch-update-default-platform":$default,services:{
        "control-plane":{image:("ghcr.io/qimaoww/qcontrol-plane:"+$tag),platform:$platform},
        "qcontrol-web":{image:("ghcr.io/qimaoww/qcontrol-web:"+$tag),platform:$platform},
        postgres:{image:"postgres:17-alpine",platform:$platform}}} | walk(if type == "object" and .platform == "" then del(.platform) else . end)'
}

export -f mock_image_role mock_target_value mock_image_manifest mock_pinned_ref mock_registry mock_compose_images mock_assert_comparison mock_local_image mock_current_image
