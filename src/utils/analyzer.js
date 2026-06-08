import { calculateRiskScore } from '../ui/summary.js';

// Mapping tables to resolve raw findings to normalized AI Agent/Tool names
const TOOL_NORMALIZER = {
  configMap: {
    'Claude Desktop': 'Claude Desktop',
    'Cursor (Global)': 'Cursor',
    'Cursor (Project)': 'Cursor',
    'Claude Code (User)': 'Claude Code',
    'Claude Code (Project)': 'Claude Code',
    'Cline (VS Code)': 'Cline',
    'Cline (VS Code Insiders)': 'Cline',
    'Cline (Global)': 'Cline',
    'Windsurf': 'Windsurf',
    'Zed Settings (Global)': 'Zed',
    'Zed Settings (Project)': 'Zed',
    'Zed Settings (Global AppData)': 'Zed',
    'Roo Code (VS Code)': 'Roo Code',
    'Roo Code (VS Code Insiders)': 'Roo Code',
    'Roo Code (Project)': 'Roo Code',
    'GitHub Copilot (Project)': 'GitHub Copilot',
    'GitHub Copilot CLI (Global)': 'GitHub Copilot CLI',
    'GitHub Copilot CLI (Project)': 'GitHub Copilot CLI',
    'GitHub Copilot (JetBrains)': 'GitHub Copilot',
    'Kiro IDE (Global)': 'Kiro',
    'Kiro IDE (Project)': 'Kiro',
    'Amazon Q (Global MCP)': 'Amazon Q Developer',
    'Amazon Q (Global Default)': 'Amazon Q Developer',
    'Amazon Q (Project MCP)': 'Amazon Q Developer',
    'Amazon Q (Project Default)': 'Amazon Q Developer',
    'Warp Terminal (Global)': 'Warp Terminal',
    'Goose (Global config.yaml)': 'Goose',
    'Goose (Global AppData)': 'Goose',
    'Goose (Global App Support)': 'Goose',
    'Aider (Global)': 'Aider',
    'Aider (Project)': 'Aider',
    'JetBrains Junie (Global)': 'JetBrains Junie',
    'JetBrains Junie (Project)': 'JetBrains Junie',
    'VS Code (Project Native MCP)': 'VS Code Native MCP',
    'VS Code Settings (Project)': 'VS Code Native MCP',
    'VS Code (Global Native MCP)': 'VS Code Native MCP',
    'VS Code (Global Insiders Native MCP)': 'VS Code Native MCP',
    'VS Code Settings (Global)': 'VS Code Native MCP',
    'Continue (Global JSON)': 'Continue.dev',
    'Continue (Project JSON)': 'Continue.dev',
    'Continue (Global YAML)': 'Continue.dev',
    'Continue (Project YAML)': 'Continue.dev',
    'Antigravity CLI Config (User)': 'Antigravity CLI',
    'Antigravity CLI Settings (User)': 'Antigravity CLI',
    'Antigravity SDK Config (User)': 'Antigravity SDK',
    'Antigravity SDK Settings (User)': 'Antigravity SDK',
    'Antigravity CLI Config (Project)': 'Antigravity CLI',
    'Antigravity CLI Settings (Project)': 'Antigravity CLI',
    'Antigravity SDK Config (Project)': 'Antigravity SDK',
    'Antigravity SDK Settings (Project)': 'Antigravity SDK',
    'OpenCode (User JSON)': 'OpenCode',
    'OpenCode (User JSONC)': 'OpenCode',
    'OpenCode (Project JSON)': 'OpenCode',
    'OpenCode (Project JSONC)': 'OpenCode',
    'OpenCode Root (Project JSON)': 'OpenCode',
    'OpenCode Root (Project JSONC)': 'OpenCode',
    'Codex CLI (User TOML)': 'OpenAI Codex CLI',
    'Codex CLI Global State (User)': 'OpenAI Codex CLI',
    'Codex CLI (Project TOML)': 'OpenAI Codex CLI',
    'OpenClaw (User)': 'OpenClaw',
    'OpenClaw (Project)': 'OpenClaw'
  },
  
  stateDirMap: {
    'Cursor IDE': 'Cursor',
    'Cursor State': 'Cursor',
    'Cline': 'Cline',
    'Cline State': 'Cline',
    'Roo Code': 'Roo Code',
    'Roo Code State': 'Roo Code',
    'Continue': 'Continue.dev',
    'Continue State': 'Continue.dev',
    'Windsurf / Codeium': 'Windsurf',
    'Claude Code': 'Claude Code',
    'Claude Code State': 'Claude Code',
    'Codex CLI': 'OpenAI Codex CLI',
    'Codex Project Config': 'OpenAI Codex CLI',
    'Antigravity CLI / SDK': 'Antigravity CLI',
    'Antigravity CLI/SDK Project Config': 'Antigravity CLI',
    'OpenCode': 'OpenCode',
    'OpenCode Project': 'OpenCode',
    'GitHub Copilot CLI': 'GitHub Copilot CLI',
    'GitHub Copilot CLI Project': 'GitHub Copilot CLI',
    'Kiro IDE': 'Kiro',
    'Kiro IDE Project': 'Kiro',
    'Amazon Q Developer': 'Amazon Q Developer',
    'Amazon Q Project': 'Amazon Q Developer',
    'Warp Terminal': 'Warp Terminal',
    'Goose': 'Goose',
    'JetBrains Junie': 'JetBrains Junie',
    'JetBrains Junie Project': 'JetBrains Junie',
    'JetBrains IDE Option Folder': 'JetBrains AI Assistant',
    'Qodo Gen (VS Code)': 'Qodo Gen',
    'Qodo Gen (VS Code Windows)': 'Qodo Gen',
    'Qodo Gen (VS Code Linux)': 'Qodo Gen',
    'GitHub Config Folder': 'GitHub Copilot',
    'Zed Project Config': 'Zed',
    'VS Code Project Config': 'VS Code Native MCP'
  },

  processMap: {
    'cursor': 'Cursor',
    'windsurf': 'Windsurf',
    'cline': 'Cline',
    'roo-code': 'Roo Code',
    'roo': 'Roo Code',
    'claude': 'Claude Desktop',
    'claude-code': 'Claude Code',
    'codex': 'OpenAI Codex CLI',
    'gemini': 'Antigravity CLI',
    'antigravity': 'Antigravity CLI',
    'aider': 'Aider',
    'opencode': 'OpenCode',
    'openclaw': 'OpenClaw',
    'kiro': 'Kiro',
    'goose': 'Goose',
    'warp': 'Warp Terminal',
    'amazon-q': 'Amazon Q Developer',
    'amazonq': 'Amazon Q Developer',
    'copilot': 'GitHub Copilot',
    'augment': 'Augment Code',
    'junie': 'JetBrains Junie',
    'qodo': 'Qodo Gen',
    'continue': 'Continue.dev',
    'zed': 'Zed',
    'trae': 'Trae',
    'ollama': 'Ollama',
    'lmstudio': 'LM Studio',
    'lm-studio': 'LM Studio',
    'jan': 'Jan.ai',
    'anythingllm': 'AnythingLLM'
  },

  historyMap: {
    'Aider CLI Agent': 'Aider',
    'Claude Code MCP configuration': 'Claude Code',
    'Claude Code execution': 'Claude Code',
    'Ollama model execution': 'Ollama',
    'Ollama daemon start': 'Ollama',
    'LM Studio': 'LM Studio',
    'CrewAI agent run': 'CrewAI',
    'LangChain script': 'LangChain',
    'Antigravity CLI invocation': 'Antigravity CLI',
    'OpenCode CLI invocation': 'OpenCode',
    'OpenClaw CLI invocation': 'OpenClaw',
    'Codex CLI invocation': 'OpenAI Codex CLI',
    'Goose CLI invocation': 'Goose',
    'Kiro CLI/IDE invocation': 'Kiro',
    'Amazon Q Developer CLI': 'Amazon Q Developer'
  }
};

/**
 * Aggregates all scan outputs and maps them to high-level Autonomous AI Capabilities and evidence.
 * Also tallies the actual unique AI agents discovered on the workstation.
 * 
 * @param {{
 *   configs: Array<any>,
 *   workspace: { detectedStateDirs: string[], detectedRuleFiles: string[], detectedModelDirs: string[] },
 *   dependencies: string[],
 *   processes: { aiProcesses: any[], runtimes: any[] },
 *   ports: Array<any>,
 *   secrets: Array<any>,
 *   history: { findings: any[], agentInvocations: string[] }
 * }} findings Raw data from all scanning engines
 * @returns {{
 *   capabilities: {
 *     toolExecution: { status: 'ACTIVE' | 'CAPABLE' | 'INACTIVE', detail: string },
 *     localInference: { status: 'ACTIVE' | 'CAPABLE' | 'INACTIVE', detail: string },
 *     workspacePresence: { status: 'DETECTED' | 'NOT DETECTED', detail: string },
 *     credentialExposure: { status: 'EXPOSED' | 'SECURE', detail: string }
 *   },
 *   evidence: string[],
 *   agents: Array<{ name: string, status: 'ACTIVE' | 'INSTALLED', evidence: string[] }>
 * }}
 */
export function analyzeCapabilities(findings) {
  const evidence = [];
  const agents = {};

  // Initialize capabilities
  /** @type {'ACTIVE' | 'CAPABLE' | 'INACTIVE'} */
  let toolExecutionStatus = 'INACTIVE';
  /** @type {'ACTIVE' | 'CAPABLE' | 'INACTIVE'} */
  let localInferenceStatus = 'INACTIVE';
  /** @type {'DETECTED' | 'NOT DETECTED'} */
  let workspacePresenceStatus = 'NOT DETECTED';
  /** @type {'EXPOSED' | 'SECURE'} */
  let credentialExposureStatus = 'SECURE';

  // Helper to initialize and retrieve an agent tally entry
  const getOrInitAgent = (name) => {
    if (!agents[name]) {
      agents[name] = {
        name,
        status: 'INSTALLED',
        evidence: []
      };
    }
    return agents[name];
  };

  // 1. Process Configurations & Servers
  const activeConfigs = findings.configs.filter(c => c.exists);
  const activeServers = activeConfigs.flatMap(c => c.servers);
  
  for (const config of activeConfigs) {
    evidence.push(`Config file discovered: ${config.tool}`);
    
    // Tally agent
    const agentName = TOOL_NORMALIZER.configMap[config.tool] || config.tool;
    const agent = getOrInitAgent(agentName);
    agent.evidence.push(`Config file: ${config.tool} (${config.scope})`);

    // If config contains active/non-disabled servers, escalate status to ACTIVE
    if (config.servers && config.servers.length > 0 && config.servers.some(s => !s.disabled)) {
      agent.status = 'ACTIVE';
    }
  }

  for (const server of activeServers) {
    if (!server.disabled) {
      toolExecutionStatus = 'ACTIVE';
      evidence.push(`MCP Server configured: '${server.name}' in ${server.type.toUpperCase()} mode`);
      if (server.autoApprove && server.autoApprove.length > 0) {
        evidence.push(`Cline Auto-Approve active for server '${server.name}' (${server.autoApprove.join(', ')})`);
      }
      if (server.type === 'jetbrains' && server.braveMode) {
        evidence.push(`JetBrains Brave Mode active (executes shell commands without prompt)`);
      }
    }
  }

  // 2. Process Workspace Artifacts
  const { detectedStateDirs, detectedRuleFiles, detectedModelDirs } = findings.workspace;
  
  for (const dir of detectedStateDirs) {
    workspacePresenceStatus = 'DETECTED';
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`Agent state directory: ${dir}`);

    // Tally agent
    // State directory name looks like "Cursor IDE directory (Global)" -> strip prefix
    const cleanDir = dir.split(' directory')[0];
    const agentName = TOOL_NORMALIZER.stateDirMap[cleanDir] || cleanDir;
    const agent = getOrInitAgent(agentName);
    agent.evidence.push(`Workspace state folder: ${dir}`);
  }

  for (const file of detectedRuleFiles) {
    workspacePresenceStatus = 'DETECTED';
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`Agent workspace rule file found: ${file}`);

    // Tally agent
    let agentName = 'Generic Agent Rules';
    const lowerFile = file.toLowerCase();
    if (lowerFile.includes('cursor')) agentName = 'Cursor';
    else if (lowerFile.includes('claude')) agentName = 'Claude Code';
    else if (lowerFile.includes('cline')) agentName = 'Cline';
    else if (lowerFile.includes('roo')) agentName = 'Roo Code';
    else if (lowerFile.includes('gemini') || lowerFile.includes('antigravity')) agentName = 'Antigravity CLI';
    else if (lowerFile.includes('windsurf')) agentName = 'Windsurf';

    const agent = getOrInitAgent(agentName);
    agent.evidence.push(`Instruction rules file: ${file}`);
  }

  for (const dir of detectedModelDirs) {
    if (localInferenceStatus === 'INACTIVE') localInferenceStatus = 'CAPABLE';
    evidence.push(`Local model cache present: ${dir}`);

    // Tally agent
    let agentName = dir.split(' Cache')[0];
    if (agentName === 'LM Studio') {
      const agent = getOrInitAgent('LM Studio');
      agent.evidence.push(`Model cache folder: ${dir}`);
    } else if (agentName === 'Ollama') {
      const agent = getOrInitAgent('Ollama');
      agent.evidence.push(`Model cache folder: ${dir}`);
    }
  }

  // 3. Process Python/JS dependencies
  for (const fw of findings.dependencies) {
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`Agent framework dependency declared: ${fw}`);

    // Tally agent (frameworks represent developer frameworks)
    const agentName = `Framework: ${fw.split(' (')[0]}`;
    const agent = getOrInitAgent(agentName);
    agent.evidence.push(`Dependency declaration: ${fw}`);
  }

  // 4. Process Running Processes
  const { aiProcesses, runtimes } = findings.processes;
  
  for (const proc of aiProcesses) {
    evidence.push(`Running AI process: ${proc.label}`);
    
    // Local inference vs general agent
    if (proc.name.includes('ollama') || proc.name.includes('lmstudio') || proc.name.includes('jan')) {
      localInferenceStatus = 'ACTIVE';
    } else {
      toolExecutionStatus = 'ACTIVE';
    }

    // Tally agent
    const agentName = TOOL_NORMALIZER.processMap[proc.name] || proc.label;
    const agent = getOrInitAgent(agentName);
    agent.evidence.push(`Active system process: ${proc.label}`);
    agent.status = 'ACTIVE';
  }

  // 5. Process Ports
  const openPorts = findings.ports.filter(p => p.open);
  for (const port of openPorts) {
    localInferenceStatus = 'ACTIVE';
    if (port.exposed) {
      evidence.push(`Local AI service exposed to network: ${port.service} on port ${port.port} (0.0.0.0)`);
    } else {
      evidence.push(`Local AI service active: ${port.service} on port ${port.port} (127.0.0.1)`);
    }

    // Tally agent
    const agent = getOrInitAgent(port.service);
    agent.evidence.push(`Active network port: ${port.port} (${port.exposed ? 'exposed' : 'local-only'})`);
    agent.status = 'ACTIVE';
  }

  // 6. Process Shell History Invocations
  for (const inv of findings.history.agentInvocations) {
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`AI agent usage history: '${inv}' command execution recorded`);

    // Tally agent
    const agentName = TOOL_NORMALIZER.historyMap[inv] || inv;
    const agent = getOrInitAgent(agentName);
    agent.evidence.push(`Shell history: '${inv}' usage log`);
  }

  // 7. Process Exposed Secrets
  const totalSecrets = findings.secrets;
  if (totalSecrets.length > 0) {
    credentialExposureStatus = 'EXPOSED';
    for (const sec of totalSecrets) {
      evidence.push(`Exposed credential: ${sec.type} found in ${sec.tool}`);

      // Tally agent
      const agentName = TOOL_NORMALIZER.configMap[sec.tool] || sec.tool;
      const agent = getOrInitAgent(agentName);
      agent.evidence.push(`Plaintext credential found: ${sec.type}`);
    }
  }

  // Map descriptions
  const toolExecutionDetail = toolExecutionStatus === 'ACTIVE' 
    ? 'Workstation is actively running agents or executing code via configured MCP tools.'
    : toolExecutionStatus === 'CAPABLE'
      ? 'Agent orchestrators or frameworks are present; workstation is configured for tool use.'
      : 'No active agent orchestrators or MCP tools configured.';

  const localInferenceDetail = localInferenceStatus === 'ACTIVE'
    ? 'Local LLM inference engines (Ollama, LM Studio) are currently active and running.'
    : localInferenceStatus === 'CAPABLE'
      ? 'Local model caches present; workstation has capability to run LLMs offline.'
      : 'No local model directories or inference engines active.';

  const workspacePresenceDetail = workspacePresenceStatus === 'DETECTED'
    ? 'Local directories contain custom instructions or workspace configs directing AI agents.'
    : 'No workspace-specific agent rule files or structures detected.';

  const credentialExposureDetail = credentialExposureStatus === 'EXPOSED'
    ? 'Plaintext API keys or database URLs detected in local configuration or history files.'
    : 'No plaintext credentials discovered in audited paths.';

  // Whitelist of valid AI agents (excludes runtimes, model servers, frameworks, and this auditor)
  const ALLOWED_AI_AGENTS = new Set([
    'Claude Desktop',
    'Claude Code',
    'Cursor',
    'Windsurf',
    'Zed',
    'Kiro',
    'GitHub Copilot',
    'GitHub Copilot CLI',
    'Cline',
    'Roo Code',
    'Continue.dev',
    'Augment Code',
    'Amazon Q Developer',
    'JetBrains AI Assistant',
    'JetBrains Junie',
    'Qodo Gen',
    'Antigravity CLI',
    'OpenAI Codex CLI',
    'Aider',
    'Goose',
    'Warp Terminal',
    'OpenCode',
    'OpenClaw',
    'Trae'
  ]);

  const agentList = Object.values(agents).filter(a => ALLOWED_AI_AGENTS.has(a.name));

  return {
    capabilities: {
      toolExecution: { status: toolExecutionStatus, detail: toolExecutionDetail },
      localInference: { status: localInferenceStatus, detail: localInferenceDetail },
      workspacePresence: { status: workspacePresenceStatus, detail: workspacePresenceDetail },
      credentialExposure: { status: credentialExposureStatus, detail: credentialExposureDetail }
    },
    evidence: Array.from(new Set(evidence)),
    agents: agentList
  };
}
