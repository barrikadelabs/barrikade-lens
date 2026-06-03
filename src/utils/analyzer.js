import { calculateRiskScore } from '../ui/summary.js';

/**
 * Aggregates all scan outputs and maps them to high-level Autonomous AI Capabilities and evidence.
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
 *   evidence: string[]
 * }}
 */
export function analyzeCapabilities(findings) {
  const evidence = [];
  
  // Initialize capabilities
  let toolExecutionStatus = 'INACTIVE';
  let localInferenceStatus = 'INACTIVE';
  let workspacePresenceStatus = 'NOT DETECTED';
  let credentialExposureStatus = 'SECURE';

  // 1. Process Configurations & Servers
  const activeConfigs = findings.configs.filter(c => c.exists);
  const activeServers = activeConfigs.flatMap(c => c.servers);
  
  for (const config of activeConfigs) {
    evidence.push(`Config file discovered: ${config.tool}`);
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
  }

  for (const file of detectedRuleFiles) {
    workspacePresenceStatus = 'DETECTED';
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`Agent workspace rule file found: ${file}`);
  }

  for (const dir of detectedModelDirs) {
    if (localInferenceStatus === 'INACTIVE') localInferenceStatus = 'CAPABLE';
    evidence.push(`Local model cache present: ${dir}`);
  }

  // 3. Process Python/JS dependencies
  for (const fw of findings.dependencies) {
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`Agent framework dependency declared: ${fw}`);
  }

  // 4. Process Running Processes
  const { aiProcesses, runtimes } = findings.processes;
  
  for (const proc of aiProcesses) {
    evidence.push(`Running AI process: ${proc.label}`);
    if (proc.name.includes('ollama') || proc.name.includes('lmstudio') || proc.name.includes('jan')) {
      localInferenceStatus = 'ACTIVE';
    } else {
      toolExecutionStatus = 'ACTIVE';
    }
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
  }

  // 6. Process Shell History Invocations
  for (const inv of findings.history.agentInvocations) {
    if (toolExecutionStatus === 'INACTIVE') toolExecutionStatus = 'CAPABLE';
    evidence.push(`AI agent usage history: '${inv}' command execution recorded`);
  }

  // 7. Process Exposed Secrets
  const totalSecrets = findings.secrets;
  if (totalSecrets.length > 0) {
    credentialExposureStatus = 'EXPOSED';
    for (const sec of totalSecrets) {
      evidence.push(`Exposed credential: ${sec.type} found in ${sec.tool}`);
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

  return {
    capabilities: {
      toolExecution: { status: toolExecutionStatus, detail: toolExecutionDetail },
      localInference: { status: localInferenceStatus, detail: localInferenceDetail },
      workspacePresence: { status: workspacePresenceStatus, detail: workspacePresenceDetail },
      credentialExposure: { status: credentialExposureStatus, detail: credentialExposureDetail }
    },
    evidence: Array.from(new Set(evidence))
  };
}
