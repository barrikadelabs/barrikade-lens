import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { getScanPaths, getAgentStateDirs, getAgentRuleFiles, getModelDirs } from '../utils/paths.js';

/**
 * Sweeps the filesystem to locate JetBrains configuration files.
 */
async function discoverJetBrainsPaths() {
  const home = os.homedir();
  const platform = os.platform();
  let jbBase = '';

  if (platform === 'darwin') {
    jbBase = path.join(home, 'Library', 'Application Support', 'JetBrains');
  } else if (platform === 'win32') {
    jbBase = path.join(process.env.APPDATA || path.join(home, 'AppData', 'Roaming'), 'JetBrains');
  } else {
    jbBase = path.join(home, '.config', 'JetBrains');
  }

  const jbPaths = [];
  try {
    const entries = await fs.readdir(jbBase, { withFileTypes: true });
    for (const entry of entries) {
      if (entry.isDirectory()) {
        const xmlPath = path.join(jbBase, entry.name, 'options', 'llm.mcpServers.xml');
        try {
          const stats = await fs.stat(xmlPath);
          if (stats.isFile()) {
            jbPaths.push({
              tool: `JetBrains (${entry.name})`,
              path: xmlPath,
              scope: 'global',
              type: 'jetbrains'
            });
          }
        } catch {
          // Skip
        }
      }
    }
  } catch {
    // JetBrains folder doesn't exist
  }
  return jbPaths;
}

/**
 * Audits all configuration files on the workstation.
 * 
 * @param {string} [cwd=process.cwd()]
 */
export async function auditConfigs(cwd = process.cwd()) {
  const resolvedPaths = getScanPaths(cwd);
  const jbPaths = await discoverJetBrainsPaths();
  const allScanPaths = [...resolvedPaths, ...jbPaths];

  const results = [];

  for (const scanConfig of allScanPaths) {
    const targetPath = scanConfig.path;
    const result = {
      tool: scanConfig.tool,
      filePath: targetPath,
      scope: scanConfig.scope,
      exists: false,
      malformed: false,
      rawContent: '',
      servers: []
    };

    try {
      const content = await fs.readFile(targetPath, 'utf8');
      result.exists = true;
      result.rawContent = content;

      if (scanConfig.type === 'jetbrains') {
        const braveModeMatches = content.includes('braveMode" value="true"') || content.includes('name="braveMode" value="true"');
        const mcpServerInfos = content.match(/<McpServerInfo>([\s\S]*?)<\/McpServerInfo>/g) || [];
        
        const servers = [];
        for (const info of mcpServerInfos) {
          const nameMatch = info.match(/name="name" value="([^"]+)"/);
          const commandMatch = info.match(/name="command" value="([^"]+)"/);
          const braveModeMatch = info.match(/name="braveMode" value="([^"]+)"/);
          
          servers.push({
            name: nameMatch ? nameMatch[1] : 'JetBrains MCP Server',
            type: 'jetbrains',
            command: commandMatch ? commandMatch[1] : undefined,
            braveMode: braveModeMatch ? braveModeMatch[1] === 'true' : braveModeMatches
          });
        }
        
        if (servers.length === 0 && content.includes('braveMode')) {
          servers.push({
            name: 'JetBrains Global Config',
            type: 'jetbrains',
            braveMode: braveModeMatches
          });
        }

        result.servers = servers;
      } else {
        // JSON Files
        let json;
        try {
          const cleanContent = content.replace(/\/\*[\s\S]*?\*\/|([^\\:]|^)\/\/.*$/gm, '$1');
          json = JSON.parse(cleanContent);
        } catch {
          result.malformed = true;
          results.push(result);
          continue;
        }

        let mcpConfig = null;
        
        if (scanConfig.type === 'mcpServers') {
          mcpConfig = json.mcpServers;
        } else if (scanConfig.type === 'vscodeServers') {
          mcpConfig = json.servers || json.mcpServers;
        } else if (scanConfig.type === 'vscodeSettings') {
          // VS Code settings can have mcp config in custom properties
          mcpConfig = json['mcp.servers'] || json['mcpServers'];
        } else if (scanConfig.type === 'continue') {
          // Continue config.json stores servers in mcpServers key (usually a list or map)
          mcpConfig = json.mcpServers;
        } else if (scanConfig.type === 'zed') {
          // Zed settings use context_servers or mcp keys
          mcpConfig = json.context_servers || json.mcp;
        } else if (scanConfig.type === 'antigravity') {
          mcpConfig = json.mcp_servers || json.mcpServers || json.servers;
        }

        if (mcpConfig) {
          if (Array.isArray(mcpConfig)) {
            mcpConfig.forEach((srv, idx) => {
              const name = srv.name || `server-${idx}`;
              const envKeys = srv.env ? Object.keys(srv.env) : [];
              const type = srv.url || srv.serverUrl ? 'sse' : 'stdio';

              result.servers.push({
                name,
                type,
                command: srv.command,
                args: srv.args || [],
                envVars: envKeys,
                url: srv.url || srv.serverUrl,
                disabled: srv.disabled === true
              });
            });
          } else if (typeof mcpConfig === 'object') {
            for (const [name, srv] of Object.entries(mcpConfig)) {
              if (srv && typeof srv === 'object') {
                const envKeys = srv.env ? Object.keys(srv.env) : [];
                const sseUrl = srv.url || srv.serverUrl || srv.server_url;
                const type = sseUrl ? 'sse' : 'stdio';
                
                result.servers.push({
                  name,
                  type,
                  command: srv.command,
                  args: srv.args || [],
                  envVars: envKeys,
                  url: sseUrl,
                  disabled: srv.disabled === true,
                  autoApprove: Array.isArray(srv.autoApprove) ? srv.autoApprove : undefined
                });
              } else if (typeof srv === 'string') {
                result.servers.push({
                  name,
                  type: 'sse',
                  url: srv
                });
              }
            }
          }
        }
      }

      results.push(result);
    } catch {
      // File does not exist, skip
    }
  }

  return results;
}

/**
 * Checks for the existence of agent state directories, rule files, and model directories.
 * 
 * @param {string} [cwd=process.cwd()]
 * @returns {Promise<{
 *   detectedStateDirs: string[],
 *   detectedRuleFiles: string[],
 *   detectedModelDirs: string[]
 * }>}
 */
export async function auditWorkspaceArtifacts(cwd = process.cwd()) {
  const stateDirs = getAgentStateDirs(cwd);
  const ruleFiles = getAgentRuleFiles(cwd);
  const modelDirs = getModelDirs();

  const detectedStateDirs = [];
  const detectedRuleFiles = [];
  const detectedModelDirs = [];

  // Check state dirs
  for (const dir of stateDirs) {
    try {
      const stat = await fs.stat(dir.path);
      if (stat.isDirectory()) {
        detectedStateDirs.push(`${dir.name} directory (${dir.scope === 'global' ? 'Global' : 'Project'})`);
      }
    } catch {
      // Doesn't exist
    }
  }

  // Check rule files
  for (const file of ruleFiles) {
    try {
      const stat = await fs.stat(file.path);
      if (stat.isFile()) {
        detectedRuleFiles.push(file.name);
      } else if (stat.isDirectory()) {
        detectedRuleFiles.push(`${file.name}/ (rules folder)`);
      }
    } catch {
      // Doesn't exist
    }
  }

  // Check model dirs
  for (const dir of modelDirs) {
    try {
      const stat = await fs.stat(dir.path);
      if (stat.isDirectory()) {
        detectedModelDirs.push(dir.name);
      }
    } catch {
      // Doesn't exist
    }
  }

  return {
    detectedStateDirs,
    detectedRuleFiles,
    detectedModelDirs
  };
}

// Known agent frameworks to match in dependencies
const AGENT_FRAMEWORKS = [
  'langchain',
  'langgraph',
  'crewai',
  'autogen',
  'pydanticai',
  'smolagents',
  'semantic-kernel',
  'haystack',
  'llama-index',
  'mastra',
  'voltagent'
];

/**
 * Inspects project files for agent framework dependencies (Tier 2).
 * 
 * @param {string} [cwd=process.cwd()] 
 * @returns {Promise<string[]>} List of discovered agent frameworks
 */
export async function auditDependencies(cwd = process.cwd()) {
  const discovered = [];

  // 1. Check package.json (JS/TS)
  try {
    const pkgContent = await fs.readFile(path.join(cwd, 'package.json'), 'utf8');
    const pkg = JSON.parse(pkgContent);
    const deps = { ...(pkg.dependencies || {}), ...(pkg.devDependencies || {}) };
    
    for (const name of Object.keys(deps)) {
      const normalized = name.toLowerCase();
      for (const fw of AGENT_FRAMEWORKS) {
        if (normalized.includes(fw)) {
          discovered.push(`${fw} (JS/TS package)`);
        }
      }
    }
  } catch {
    // package.json doesn't exist or is malformed
  }

  // 2. Check requirements.txt (Python)
  try {
    const reqs = await fs.readFile(path.join(cwd, 'requirements.txt'), 'utf8');
    const lines = reqs.toLowerCase().split('\n');
    for (const line of lines) {
      const trimmed = line.split(/[=<>]/)[0].trim();
      for (const fw of AGENT_FRAMEWORKS) {
        if (trimmed === fw || trimmed.replace('-', '') === fw) {
          discovered.push(`${fw} (Python library)`);
        }
      }
    }
  } catch {
    // requirements.txt doesn't exist
  }

  // 3. Check pyproject.toml (Python)
  try {
    const toml = await fs.readFile(path.join(cwd, 'pyproject.toml'), 'utf8');
    const tomlLower = toml.toLowerCase();
    for (const fw of AGENT_FRAMEWORKS) {
      if (tomlLower.includes(fw)) {
        discovered.push(`${fw} (Python dependency)`);
      }
    }
  } catch {
    // pyproject.toml doesn't exist
  }

  return Array.from(new Set(discovered));
}
