# Install and load

Download and extract the panel's management Skill ZIP. From the extracted `qcontrolhub-manager` directory run `python3 scripts/install.py CLIENT --project /path/to/workspace`. Choose the agent's actual project or execution workspace. The installer prints `files_installed`. Open a fresh session, check the client's Skill catalog for `qcontrolhub-manager`, then invoke it and verify the authorized `GET /api-key` separately.

| CLIENT | Project installation directory |
| --- | --- |
| codex, pi | `.agents/skills` |
| claude | `.claude/skills` |
| gemini | `.gemini/skills` |
| copilot | `.github/skills` |
| opencode | `.opencode/skills` |
| cursor | `.cursor/skills` |
| dsh | `.dsh/skills` in the nearest project root |
| hermes | `.hermes/skills` in the nearest Git root |
| openclaw | `skills` in the active agent workspace |

Codex also supports a user install in `~/.agents/skills/`. DSH treats an empty ancestor `.git` as a project root. Hermes loads project skills in trusted checkouts. Confirm the project trust decision in Hermes and use `/qcontrolhub-manager` in the project session. For OpenClaw inspect `openclaw skills info qcontrolhub-manager` in the selected workspace. Each client's Python execution environment must receive the configured panel origin and Key.

DSH headless can filter `QCH_API_KEY` from Bash. For its authorized read, use the installed `references/dsh-readonly.patch.yml` with the headless profile. The patch passes exactly the two named variables from the launcher to the per-tool process. The following invocation uses the group's installed location. Substitute the actual project root.

```sh
dsh headless --patch /path/to/project/.dsh/skills/qcontrolhub-manager/references/dsh-readonly.patch.yml \
  'Load qcontrolhub-manager and use mcp__qch__authorization to check effective grants.'
```
