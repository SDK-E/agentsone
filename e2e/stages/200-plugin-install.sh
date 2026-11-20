# SPDX-License-Identifier: MIT
# shellcheck shell=bash

stage plugin-install "a human installs a sample plugin, it shows enabled, and its agent template appears and goes with it"

# The sample plugin is e2e/fixtures/sample-plugin: one agent template. Its part works when the
# template is offered for hiring while the plugin is enabled, and not otherwise.
stage_plugin_install() {
  needs plugin-install machine edge || return 0
  door || return 0
  human_signin plugin-install || return 0
  local src=/tmp/e2e-sample-plugin id founder
  local present='[.items[] | select(.name == "e2e-greeter")] | length == 1'
  local absent='[.items[] | select(.name == "e2e-greeter")] | length == 0'

  # A clean start, and the sample inside the machine as a folder, where the daemon reads it.
  in_machine rm -rf "$src"
  expect ok "copy the sample plugin into the machine" compose cp e2e/fixtures/sample-plugin "machine:${src}"
  human_json "the bundled Small Seed plugin is installed and enabled" 200 \
    '[.items[] | select(.name == "small-seed" and .enabled == true and .health == "ok")] | length == 1' \
    from_browser GET /v1/plugins
  human_json "before the install the sample plugin is not listed" 200 \
    '[.items[] | select(.name == "e2e-sample")] | length == 0' from_browser GET /v1/plugins
  human_json "before the install its agent template is not offered" 200 "$absent" \
    from_browser GET /v1/agent-templates

  id="$(from_browser POST /v1/plugins -d "$(jq -nc --arg r "$src" '{source: "agentsone", ref: $r, enable: true}')" |
    jq -er 'select(.name == "e2e-sample" and .enabled == true and .health == "ok" and .version == "1.0.0" and (.kinds | index("agent-template")) != null and .installed_by == "human:'"$HUMAN"'") | .id')" || id=""
  if [ -z "$id" ]; then
    fail "a human installs the sample plugin: enabled, healthy, an agent-template plugin installed by the human" \
      "$(from_browser GET /v1/plugins)"
    in_machine rm -rf "$src"
    return 0
  fi
  pass "a human installs the sample plugin: enabled, healthy, installed by the human ($id)"

  human_json "the plugin shows enabled when read by its id" 200 '.name == "e2e-sample" and .enabled == true' \
    from_browser GET "/v1/plugins/${id}"
  human_json "the enabled list holds the sample plugin" 200 '[.items[] | select(.id == "'"$id"'")] | length == 1' \
    from_browser GET '/v1/plugins?enabled=true'
  human_json "its agent template is offered for hiring" 200 "$present" from_browser GET /v1/agent-templates
  human_json "installing the same version again is refused" 409 '.status == 409' \
    from_browser POST /v1/plugins -d "$(jq -nc --arg r "$src" '{source: "agentsone", ref: $r, enable: true}')"

  if ! founder="$(human_token founder '["plugins:write"]')"; then
    fail "plugin-install: the human cannot issue a founder token"
  else
    human_json "an agent cannot install from a path on the machine" 403 '.status == 403' \
      human_as_agent "$founder" POST /plugins -d "$(jq -nc --arg r "$src" '{source: "agentsone", ref: $r, enable: true}')"
  fi

  human_json "a human disables the plugin" 200 '.enabled == false' from_browser POST "/v1/plugins/${id}/disable"
  human_json "disabled: its agent template is no longer offered" 200 "$absent" from_browser GET /v1/agent-templates
  human_json "a human enables the plugin again" 200 '.enabled == true and .health == "ok"' \
    from_browser POST "/v1/plugins/${id}/enable"
  human_json "enabled again: its agent template is offered" 200 "$present" from_browser GET /v1/agent-templates

  human_json "a human uninstalls the plugin" 204 '' from_browser DELETE "/v1/plugins/${id}"
  human_json "uninstalled: the plugin is gone" 404 '.status == 404' from_browser GET "/v1/plugins/${id}"
  human_json "uninstalled: its agent template is gone" 200 "$absent" from_browser GET /v1/agent-templates
  in_machine rm -rf "$src"
}
