# flow shell wrapper for zsh. Source via:  eval "$(flow shell init zsh)"
flow() {
    local _flow_bin
    _flow_bin="$(command which flow 2>/dev/null)"
    if [ -z "$_flow_bin" ]; then
        echo "flow: binary not found on PATH" >&2
        return 127
    fi

    case "$1" in
        jump|j)
            local _out _line _rc
            _out="$("$_flow_bin" "$@")"
            _rc=$?
            if [ $_rc -ne 0 ]; then
                printf '%s\n' "$_out" >&2
                return $_rc
            fi
            while IFS= read -r _line; do
                case "$_line" in
                    __FLOW_CD__:*)
                        cd "${_line#__FLOW_CD__:}" || return $?
                        ;;
                    *)
                        printf '%s\n' "$_line"
                        ;;
                esac
            done <<< "$_out"
            return 0
            ;;
        *)
            "$_flow_bin" "$@"
            ;;
    esac
}
