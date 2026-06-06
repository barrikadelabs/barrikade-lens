# Barrikade Lens

> **Instant Shadow AI discovery & security audit for developer workstations.**
> Scans for AI agents, MCP servers, local LLMs, and plaintext secrets — in seconds.

```bash
npx barrikade-lens
```

No signup. No API keys. No configuration. Just run it.

---

## What It Does

Barrikade Lens gives you (and your security team) instant visibility into the **Shadow AI footprint** on any developer machine:

### AI Agent Discovery
Discovers **23+ AI tools** across your system — config files, running processes, state directories, and workspace artifacts:

- **IDE Agents**: Cursor, GitHub Copilot, Windsurf, Cline, Roo Code, Kiro, Continue.dev, Augment Code, Qodo Gen
- **CLI Agents**: Claude Code, Antigravity CLI, OpenCode, Codex CLI, OpenClaw, Aider, Goose
- **Desktop AI**: Claude Desktop
- **IDE Plugins**: JetBrains Junie, Amazon Q, Zed AI, Warp Terminal

### MCP Server Audit
Parses all discovered configuration files to identify configured **Model Context Protocol (MCP) servers**, including their transport types, command arguments, and environment variables.

### Local LLM Port Sweep
Checks common ports for local inference servers:
- **Ollama** (11434), **LM Studio** (1234), **Jan.ai** (1337)
- **LocalAI / Llama.cpp** (8080 / 8000), **Text Gen WebUI** (7860 / 5000)
- **JetBrains MCP** (63334, 64342+), **Figma MCP** (3845)

Flags servers bound to `0.0.0.0` (network-exposed) as **CRITICAL** risks.

### Plaintext Secrets Scanner
Detects hardcoded credentials across configs, `.env` files, and shell history:
- API Keys: OpenAI, Anthropic, HuggingFace, Google, Slack, Stripe
- AWS Access Keys & STS tokens
- GitHub PATs (classic & fine-grained)
- Database connection strings (PostgreSQL, MongoDB)
- Private key blocks

### Risk Flags
- JetBrains **Brave Mode** (auto-execute without approval)
- Cline **Auto-Approve** settings
- Exposed inference server ports

---

## Usage

```
Usage: barrikade-lens [options]

Instant Shadow AI agent discovery & security scanner

Options:
  -V, --version        output the version number
  --json               Output raw JSON instead of interactive dashboard
  -r, --report <path>  Write JSON scan report to specified file path
  --html <path>        Generate a self-contained HTML CISO audit report
  --no-telemetry       Disable anonymous high-level telemetry reporting
  --verbose            Show detailed execution logs
  -h, --help           display help for command
```

### Examples

**Run a quick scan:**
```bash
npx barrikade-lens
```

**Export an HTML report for your CISO:**
```bash
npx barrikade-lens --html report.html
```

**CI/CD pipeline (exits code 1 if critical issues found):**
```bash
npx barrikade-lens --json --report audit.json
```

**Install globally:**
```bash
npm install -g barrikade-lens
barrikade-lens
```

---

## Privacy & Telemetry

- **100% local scanning.** The scan engine runs entirely on your machine.
- **No code exposure.** Your source code, file paths, and raw credential values never leave your system.
- **Anonymous metrics only.** High-level counts (e.g., "3 agents found, 1 exposed port") are sent to `https://api.barrikade.ai/lens/telemetry`. You see and approve the exact record before it's sent.
- **Opt out anytime:**
  ```bash
  npx barrikade-lens --no-telemetry
  # or
  export BARRIKADE_NO_TELEMETRY=1
  ```

---

## Fleet-Wide Governance

Individual scans protect one machine. To govern Shadow AI across your entire organisation:

- Fleet-wide developer AI agent discovery
- Secure credential proxying and RBAC policies for MCP servers
- Command execution guardrails and safety policy enforcement
- Compliance auditing for developer-installed AI extensions

👉 Learn more at [barrikade.ai](https://barrikade.ai)
