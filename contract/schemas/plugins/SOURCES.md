# Plugin sources

Agents One installs plugins from four kinds of source. Three adapters read foreign formats and write the
normalised manifest, `agentsone.plugin.json` ([`plugin-manifest.schema.json`](../plugin-manifest.schema.json)),
into the installed plugin's root. The fourth kind is a plugin that already ships that file. After
installation only the normalised manifest is read.

The source formats below were checked against their public documentation on 19 November 2026:

- Claude Code plugins: <https://code.claude.com/docs/en/plugins-reference> and
  <https://code.claude.com/docs/en/plugins/marketplace-reference.md>
- Agent Skills: <https://agentskills.io/specification>
- MCP registry: <https://registry.modelcontextprotocol.io/v0.1/servers> and the server.json schema
  `https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json`

## Rules every adapter follows

- `source.kind` names the adapter; `source.ref` records exactly what was installed.
- All seven `contents` lists are written, empty when the source has nothing of that kind.
- Paths are rewritten to start with `./` or `${PLUGIN_ROOT}/`. A path that leaves the plugin root, or a
  segment that starts with a dot, is refused.
- `${CLAUDE_PLUGIN_ROOT}` becomes `${PLUGIN_ROOT}` everywhere: paths, `command`, `args`, `env` and `url`.
- A missing version becomes `0.0.0`; a version that is not semantic is refused.
- A missing description becomes the first line of the plugin's README, cut to 255 characters, or the
  plugin name.
- A missing licence becomes `NOASSERTION` (an SPDX value meaning "not stated").
- `compat.agentsone` is `>=0.1.0` unless the source says otherwise.
- Parts the adapter cannot map are listed in the install report and left out; they never fail the install
  silently.

## Claude Code plugins (`source.kind: claude-code`)

### Plugin manifest: `.claude-plugin/plugin.json`

The manifest is optional. Only `name` is required; it is kebab-case with no spaces. Without a manifest,
Claude Code reads the standard layout (`skills/`, `commands/`, `agents/`, `hooks/hooks.json`,
`.mcp.json`) and takes the name from the marketplace entry or the directory.

| Claude Code | Agents One manifest |
| --- | --- |
| `name` | `name` |
| `version` (any string) | `version` (must be semantic) |
| `description` | `description` |
| `author` `{name, email?, url?}` | `author` |
| `homepage` | `homepage` |
| `license` (SPDX) | `license` |
| `skills` (directories of `<name>/SKILL.md`, plus default `skills/`) | `contents.skills`, one entry per `SKILL.md`, read as an Agent Skill (below) |
| `agents` (Markdown files, default `agents/`) | `contents.agentTemplates`: each agent Markdown file becomes an agent template with `kind: chat`, `isRoot: false`, the body as `charter`, frontmatter `description` as `oneLine`, runner `claude`, and default limits |
| `mcpServers` (inline map, `.mcp.json`, or file paths) | `contents.mcpServers`: `command` + `args` + `env` become `transport: stdio`; `type: http` with `url` becomes `transport: streamable-http`; `sse` and `.mcpb` bundles are reported as not mapped |
| `commands` (Markdown command files) | Not mapped; listed in the install report. Agents One has no slash commands. |
| `hooks` | Not mapped; listed in the install report. Agents One has no Claude Code hook events. |
| `lspServers`, `outputStyles`, `workflows`, `channels`, `userConfig`, `experimental` | Not mapped; listed in the install report. |
| `experimental.themes` | Not mapped: Claude Code themes are terminal themes, not Agents One themes. |

### Marketplace: `.claude-plugin/marketplace.json`

A marketplace lists plugins. Required: `name`, `owner` (`owner.name` required, `email` and `url`
optional), and `plugins`. Each plugin entry requires `name` and `source`; it may also carry any
`plugin.json` field, which applies when the plugin has no manifest of its own.

Agents One reads the marketplace to offer its plugins on the Plugins page. `source.ref` is
`<marketplace name>/<plugin name>`. Entry `source` types:

| Source | Fields | Fetched as |
| --- | --- | --- |
| Relative path string | `"./plugins/x"`, from the marketplace root | Directory inside the marketplace checkout |
| `github` | `repo` (`owner/repo`), `ref`, `sha` | Git clone over HTTPS |
| `url` | `url` (any git URL), `ref`, `sha` | Git clone |
| `git-subdir` | `url`, `path`, `ref`, `sha` | Sparse git checkout of `path` |
| `npm` | `package`, `version`, `registry` | npm tarball, unpacked without running scripts |
| `archive` | `url` (HTTPS zip), `sha256` | Download; `sha256` copied to `source.sha256` |
| `command` | `command`, `timeout`, `mode` | Refused: Agents One does not run marketplace-supplied commands at install time |

Every fetch host must be on the host tool's allow list.

## Agent Skills (`source.kind: agent-skills`)

A skill is a directory with a `SKILL.md` file: YAML frontmatter, then Markdown instructions. Optional
folders are `scripts/`, `references/` and `assets/`.

| `SKILL.md` frontmatter | Rule | Agents One manifest |
| --- | --- | --- |
| `name` (required) | 1 to 64 characters, lowercase letters, digits and single hyphens, not at the start or end, and **equal to the directory name** | `contents.skills[].name` and the plugin `name` |
| `description` (required) | 1 to 1024 characters | `contents.skills[].description`, and the first 255 characters as plugin `description` |
| `license` | Free text | `license` when it is an SPDX expression, otherwise `NOASSERTION` |
| `compatibility` | Up to 500 characters | Shown in the install report |
| `metadata` | String-to-string map | `metadata.version`, when semantic, becomes `version` |
| `allowed-tools` | Space-separated, experimental | Not mapped; tools come from MCP servers |

A repository holding several skill directories becomes one plugin with one `contents.skills` entry per
directory; the plugin is named after the repository.

## MCP registry (`source.kind: mcp-registry`)

The registry lists servers at `GET https://registry.modelcontextprotocol.io/v0.1/servers`. The response is
`{"servers": [{"server": <server.json>, "_meta": {...}}], "metadata": {"nextCursor": "...", "count": n}}`;
pass `cursor` to page and `search` to filter. `_meta["io.modelcontextprotocol.registry/official"]` carries
`status`, `publishedAt`, `updatedAt` and `isLatest`. Only `status: active` entries with `isLatest: true`
are offered.

Each `server.json` names a server in reverse-DNS form (`io.github.owner/name`) with `description`,
`version`, optional `title`, `repository` and `websiteUrl`, and either `packages[]`, `remotes[]` or both.

| server.json | Agents One manifest |
| --- | --- |
| `name` | `name`: the part after the last `/`, lowercased, non-matching characters replaced by `-`; `source.ref` is `<name>@<version>` |
| `version` | `version` |
| `description` | `description` |
| `title` | Shown as the Plugins page title |
| `websiteUrl` or `repository.url` | `homepage` |
| `remotes[]` with `type: streamable-http` | `contents.mcpServers[]` with `transport: streamable-http`, `url`; `{variable}` placeholders and `headers` become settings the installing human or agent fills in |
| `remotes[]` with `type: sse` | Not mapped; reported |
| `packages[]` with `registryType: npm` and `transport.type: stdio` | `transport: stdio`, `command: npx`, `args: ["-y", "<identifier>@<version>", ...packageArguments]`, `env` from `environmentVariables` |
| `packages[]` with `registryType: pypi` and stdio | `command: uvx`, `args: ["<identifier>==<version>", ...]` |
| `packages[]` with `registryType: oci` and stdio | `command: podman`, `args: ["run", "-i", "--rm", "<identifier>"]` when a container engine is installed; otherwise reported |
| `packages[]` with `registryType` `nuget`, `cargo` or `mcpb` | Not mapped in version 1; reported |

The registry gives no licence field, so `license` is `NOASSERTION` unless the repository says otherwise.
Secrets named in `environmentVariables` with `isSecret: true` are never written into the manifest; the
daemon asks for them when the server is enabled.

## Agents One plugins (`source.kind: agentsone`)

A plugin that ships `agentsone.plugin.json` at its root is used as is after schema validation. `source.ref`
is the git URL, HTTPS archive URL or local path it was installed from.
