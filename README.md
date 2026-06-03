# Barrikade Lens (Local Audit CLI)

`barrikade-audit` is a zero-install, privacy-first security auditor for local developer workstations. It scans your environment for active AI agent tools, exposed local inference servers, and plaintext API keys or credentials.

It's designed to give developers and security engineers an instant, transparent view of their local "Shadow AI" footprint.

## Quick Start

You can run the audit immediately without signing up, configuring API keys, or installing anything:

```bash
npx barrikade-audit
```

Within seconds, the CLI scans your workstation and displays an interactive audit dashboard directly in your terminal.

---

## What the Scanner Checks

### 1. Shadow Agent & MCP Configuration Auditor
The tool scans default system paths to discover configuration files for popular AI tools and clients, identifying all configured Model Context Protocol (MCP) servers:
* **Claude Desktop** (`claude_desktop_config.json`)
* **Cursor IDE** (Global `~/.cursor/mcp.json` and project-level `.cursor/mcp.json`)
* **Claude Code** (Global `~/.claude.json` and project-level `.mcp.json`)
* **Cline** (VS Code extension configurations, including traditional and Insiders)
* **Windsurf** (`mcp_config.json`)
* **VS Code Native MCP** (Workspace `.vscode/mcp.json` and global configurations)
* **Antigravity SDK** (Global and project-level config files)

### 2. Local LLM Server Port Sweep
The tool performs a fast, parallel socket connection sweep against ports commonly used by local AI inference backends:
* **Ollama** (Port `11434`)
* **LM Studio** (Port `1234`)
* **Jan.ai** (Port `1337`)
* **LocalAI / Llama.cpp** (Port `8080` / `8000`)
* **Text Generation WebUI** (Port `7860` / `5000`)
* **Figma Desktop MCP Server** (Port `3845`)
* **JetBrains built-in MCP Servers** (Ports `63334`, `64342+`)

**Binding Exposure Verification:**
If a server is running, the tool checks whether it is bound securely to `127.0.0.1` (loopback - accessible only to your machine) or exposed on `0.0.0.0` (all interfaces - accessible to anyone on your local area network (LAN)). Unintentionally exposed ports are flagged as **CRITICAL** risks.

### 3. Plaintext Secrets & CISO Risk Analysis
The CLI parses configuration contents using high-efficiency regex patterns to detect hardcoded plaintext credentials routinely stored in configuration blocks:
* **API Keys:** OpenAI, Anthropic, HuggingFace, Google API, Slack, Stripe
* **Access Keys:** AWS Access Key ID, AWS STS temporary credentials
* **Tokens:** GitHub classic & fine-grained personal access tokens
* **Connection Strings:** PostgreSQL, MongoDB
* **Private Keys:** `-----BEGIN PRIVATE KEY-----` blocks
* **Permission Flags:** JetBrains **Brave Mode** (executing commands without user approval) and Cline **Auto-Approve** options.

---

## Options & Flags

```
Usage: barrikade-audit [options]

Instant Shadow AI & MCP server security scanner

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

**Export a shareable CISO HTML report:**
```bash
npx barrikade-audit --html report.html
```

**Run in CI/CD pipeline and output raw JSON (exits with code 1 if critical issues are found):**
```bash
npx barrikade-audit --json --report build-audit.json
```

---

## Privacy & Telemetry Commitment

* **Local Scanning:** The scan engine executes entirely on your machine.
* **No Code Exposure:** Your source code, configuration files, and raw values never leave your system.
* **Anonymized Findings:** In order to track tool effectiveness, high-level metrics are sent to `https://api.barrikade.ai/telemetry` (e.g., number of open ports, total secrets detected). All credentials and file paths are heavily redacted before telemetry generation.
* **How to Opt Out:** You can disable telemetry at any time by appending `--no-telemetry` to your command or by setting the environment variable `BARRIKADE_NO_TELEMETRY=1`.

---

## Fleet-Wide Governance

Individual scans protect one machine. To govern Shadow AI across your entire organization, check out **Barrikade**:
* Fleet-wide developer AI agent discovery
* Secure credential proxying and RBAC policies for MCP servers
* Command execution guardrails and safety policy enforcement
* Compliance auditing for developer-installed AI extensions

👉 Learn more and request access at [barrikade.ai](https://barrikade.ai).
