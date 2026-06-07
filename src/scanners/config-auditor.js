import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { getScanPaths, getAgentStateDirs, getAgentRuleFiles, getModelDirs } from '../utils/paths.js';

/**
 * Parses simple TOML content line by line (used for Codex CLI config.toml).
 * Extracts mcp_servers blocks.
 * 
 * @param {string} tomlContent 
 * @returns {any}
 */
function parseToml(tomlContent) {
  try {
    const lines = tomlContent.split(/\r?\n/);
    const data = { mcpServers: {} };
    let currentServer = null;

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#') || trimmed.startsWith(';')) continue;

      const headerMatch = trimmed.match(/^\[mcp_servers\.([^\]]+)\]/);
      if (headerMatch) {
        currentServer = headerMatch[1].trim();
        data.mcpServers[currentServer] = { command: '', args: [], env: {} };
        continue;
      }

      if (trimmed.startsWith('[') && trimmed.endsWith(']')) {
        currentServer = null;
        continue;
      }

      if (currentServer && data.mcpServers[currentServer]) {
        const eqIdx = trimmed.indexOf('=');
        if (eqIdx !== -1) {
          const key = trimmed.slice(0, eqIdx).trim();
          const val = trimmed.slice(eqIdx + 1).trim();

          if (key === 'command') {
            data.mcpServers[currentServer].command = val.replace(/^['"]|['"]$/g, '');
          } else if (key === 'args') {
            if (val.startsWith('[') && val.endsWith(']')) {
              data.mcpServers[currentServer].args = val
                .slice(1, -1)
                .split(',')
                .map(s => s.trim().replace(/^['"]|['"]$/g, ''))
                .filter(s => s !== '');
            }
          } else if (key === 'env') {
            if (val.startsWith('{') && val.endsWith('}')) {
              const body = val.slice(1, -1);
              const pairs = body.split(',');
              for (const pair of pairs) {
                const eq = pair.indexOf('=');
                if (eq !== -1) {
                  const k = pair.slice(0, eq).trim();
                  const v = pair.slice(eq + 1).trim().replace(/^['"]|['"]$/g, '');
                  data.mcpServers[currentServer].env[k] = v;
                }
              }
            }
          }
        }
      }
    }
    return data;
  } catch (err) {
    throw new Error('Malformed TOML: ' + err.message);
  }
}

/**
 * Parses simple YAML content line by line (used for Goose, Aider, and Continue).
 * Extracts mcpServers or extensions blocks.
 * 
 * @param {string} yamlContent 
 * @returns {any}
 */
function parseYaml(yamlContent) {
  try {
    const lines = yamlContent.split(/\r?\n/);
    const data = { mcpServers: {} };
    let currentServer = null;
    let serverIndent = -1;
    let inMcp = false;
    let mcpIndent = -1;

    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;

      const indent = line.length - line.trimStart().length;

      if (inMcp && indent <= mcpIndent && trimmed && !trimmed.startsWith('-') && !trimmed.includes(':')) {
        inMcp = false;
        currentServer = null;
      }

      if (!inMcp) {
        const colonIdx = trimmed.indexOf(':');
        if (colonIdx !== -1) {
          const key = trimmed.slice(0, colonIdx).trim();
          if (key === 'mcpServers' || key === 'mcp_servers' || key === 'servers' || key === 'extensions') {
            inMcp = true;
            mcpIndent = indent;
          }
        }
        continue;
      }

      // Inside MCP block
      if (currentServer && indent <= serverIndent && !trimmed.startsWith('-')) {
        currentServer = null;
      }

      if (!currentServer && trimmed.endsWith(':')) {
        currentServer = trimmed.slice(0, -1).trim();
        serverIndent = indent;
        data.mcpServers[currentServer] = { command: '', args: [], env: {} };
        continue;
      }

      if (currentServer) {
        const srv = data.mcpServers[currentServer];

        if (trimmed.startsWith('-')) {
          const val = trimmed.slice(1).trim().replace(/^['"]|['"]$/g, '');
          if (val) {
            srv.args.push(val);
          }
          continue;
        }

        const colonIdx = trimmed.indexOf(':');
        if (colonIdx !== -1) {
          const key = trimmed.slice(0, colonIdx).trim();
          const val = trimmed.slice(colonIdx + 1).trim().replace(/^['"]|['"]$/g, '');

          if (key === 'command' || key === 'cmd') {
            srv.command = val;
          } else if (key === 'args') {
            if (val && val.startsWith('[') && val.endsWith(']')) {
              srv.args = val
                .slice(1, -1)
                .split(',')
                .map(s => s.trim().replace(/^['"]|['"]$/g, ''))
                .filter(s => s);
            }
          } else if (key === 'enabled' && val === 'false') {
            srv.disabled = true;
          } else if (key === 'url' || key === 'serverUrl' || key === 'server_url') {
            srv.url = val;
          } else if (indent > serverIndent) {
            // Check for env properties
            if (key !== 'env') {
              srv.env[key] = val;
            }
          }
        }
      }
    }
    return data;
  } catch (err) {
    throw new Error('Malformed YAML: ' + err.message);
  }
}

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
 * Dynamically checks for server configurations inside .continue/mcpServers/ directory.
 * 
 * @param {string} cwd 
 * @returns {Promise<Array<{ tool: string, path: string, scope: 'global' | 'project', type: string }>>}
 */
async function discoverContinueMcpServers(cwd = process.cwd()) {
  const home = os.homedir();
  const dirsToCheck = [
    { dir: path.join(home, '.continue', 'mcpServers'), scope: 'global' },
    { dir: path.join(cwd, '.continue', 'mcpServers'), scope: 'project' }
  ];
  
  const foundConfigs = [];
  
  for (const { dir, scope } of dirsToCheck) {
    try {
      const entries = await fs.readdir(dir, { withFileTypes: true });
      for (const entry of entries) {
        if (entry.isFile()) {
          const ext = path.extname(entry.name).toLowerCase();
          if (ext === '.json' || ext === '.yaml' || ext === '.yml' || ext === '.jsonc') {
            const fullPath = path.join(dir, entry.name);
            const parserType = (ext === '.yaml' || ext === '.yml') ? 'yaml' : (ext === '.jsonc' ? 'jsonc' : 'mcpServers');
            foundConfigs.push({
              tool: `Continue MCP Server Config (${entry.name})`,
              path: fullPath,
              scope,
              type: parserType
            });
          }
        }
      }
    } catch {
      // Directory doesn't exist, skip
    }
  }
  
  return foundConfigs;
}

/**
 * Audits all configuration files on the workstation.
 * 
 * @param {string} [cwd=process.cwd()]
 */
export async function auditConfigs(cwd = process.cwd()) {
  const resolvedPaths = getScanPaths(cwd);
  const jbPaths = await discoverJetBrainsPaths();
  const continuePaths = await discoverContinueMcpServers(cwd);
  const allScanPaths = [...resolvedPaths, ...jbPaths, ...continuePaths];

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
        // Parse YAML, TOML, or JSON
        let json;
        try {
          if (scanConfig.type === 'toml') {
            json = parseToml(content);
          } else if (scanConfig.type === 'yaml') {
            json = parseYaml(content);
          } else {
            // Strip JSON/JSONC comments and trailing commas
            const cleanContent = content
              .replace(/\/\*[\s\S]*?\*\/|([^\\:]|^)\/\/.*$/gm, '$1')
              .replace(/,(\s*[\]}])/g, '$1');
            json = JSON.parse(cleanContent);
          }
        } catch {
          result.malformed = true;
          results.push(result);
          continue;
        }

        let mcpConfig = null;
        
        if (scanConfig.type === 'toml' || scanConfig.type === 'yaml') {
          mcpConfig = json.mcpServers;
        } else if (scanConfig.type === 'mcpServers') {
          mcpConfig = json.mcpServers;
        } else if (scanConfig.type === 'vscodeServers') {
          mcpConfig = json.servers || json.mcpServers;
        } else if (scanConfig.type === 'vscodeSettings') {
          // VS Code settings can have mcp config in custom properties
          mcpConfig = json['mcp.servers'] || json['mcpServers'] || json['augment.advanced.mcpServers'] || (json['augment.advanced'] && json['augment.advanced'].mcpServers);
        } else if (scanConfig.type === 'continue') {
          mcpConfig = json.mcpServers;
        } else if (scanConfig.type === 'zed') {
          mcpConfig = json.context_servers || json.mcp;
        } else if (scanConfig.type === 'antigravity' || scanConfig.type === 'antigravityCli') {
          mcpConfig = json.mcp_servers || json.mcpServers || json.servers;
        } else if (scanConfig.type === 'opencode' || scanConfig.type === 'jsonc') {
          mcpConfig = json.mcpServers || json.mcp_servers || json.servers;
        } else if (scanConfig.type === 'openclaw') {
          mcpConfig = json.mcpServers || json.mcp_servers || json.servers;
        } else if (scanConfig.type === 'codex') {
          mcpConfig = json.mcpServers || json.mcp_servers || json.servers;
        } else if (scanConfig.type === 'amazonq') {
          mcpConfig = json.mcpServers || json.mcp_servers || json.servers;
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
                command: srv.command || srv.cmd,
                args: srv.args || [],
                envVars: envKeys,
                url: srv.url || srv.serverUrl,
                disabled: srv.disabled === true || srv.enabled === false
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
                  command: srv.command || srv.cmd,
                  args: srv.args || [],
                  envVars: envKeys,
                  url: sseUrl,
                  disabled: srv.disabled === true || srv.enabled === false,
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
        detectedStateDirs.push({ name: dir.name, path: dir.path, scope: dir.scope });
      }
    } catch {
      // Doesn't exist
    }
  }

  // Check rule files
  for (const file of ruleFiles) {
    try {
      const stat = await fs.stat(file.path);
      if (stat.isFile() || stat.isDirectory()) {
        detectedRuleFiles.push({ name: file.name, path: file.path, isDir: stat.isDirectory() });
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
        detectedModelDirs.push({ name: dir.name, path: dir.path });
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

/**
 * Scans for common AI browser extensions and sidebars in local browser profiles (Chrome, Brave, Arc).
 * 
 * @returns {Promise<Array<{ browser: string, name: string, id: string, path: string }>>}
 */
export async function auditBrowserExtensions() {
  const home = os.homedir();
  const platform = os.platform();
  
  const extensionIds = {
    'ghcolbpknhaijonghidnofeggoaafoag': 'Monica AI Sidebar',
    'aaffdilidmcojipgfkfippocackoenpl': 'Harpa AI Automation',
    'camppjleccjlonihdilgglamonnogidm': 'Merlin AI Assistant',
    'mhnccdjjolichadneaocbjfojocmglhi': 'MaxAI.me',
    'fihnjombegmojnicobbleocnkbocjfgo': 'Sider AI Sidebar',
    'oobmeephbghjdbhhonodagkocokhdgda': 'ChatGPT Writer',
    'difoiogiljbhcecojaccikinmennhkap': 'Sider AI (Alt)',
    'panlhjlepmfkgmghbhjdedmjdailffgd': 'Claude Sidebar / Extension'
  };

  const basePaths = [];

  if (platform === 'darwin') {
    basePaths.push(
      { browser: 'Chrome', path: path.join(home, 'Library', 'Application Support', 'Google', 'Chrome', 'Default', 'Extensions') },
      { browser: 'Brave', path: path.join(home, 'Library', 'Application Support', 'BraveSoftware', 'Brave-Browser', 'Default', 'Extensions') },
      { browser: 'Arc', path: path.join(home, 'Library', 'Application Support', 'Arc', 'User Data', 'Default', 'Extensions') }
    );
  } else if (platform === 'win32') {
    const localAppData = process.env.LOCALAPPDATA || path.join(home, 'AppData', 'Local');
    basePaths.push(
      { browser: 'Chrome', path: path.join(localAppData, 'Google', 'Chrome', 'User Data', 'Default', 'Extensions') },
      { browser: 'Brave', path: path.join(localAppData, 'BraveSoftware', 'Brave-Browser', 'User Data', 'Default', 'Extensions') }
    );
  } else {
    basePaths.push(
      { browser: 'Chrome', path: path.join(home, '.config', 'google-chrome', 'Default', 'Extensions') },
      { browser: 'Brave', path: path.join(home, '.config', 'BraveSoftware', 'Brave-Browser', 'Default', 'Extensions') }
    );
  }

  const detected = [];
  for (const { browser, path: basePath } of basePaths) {
    for (const [id, name] of Object.entries(extensionIds)) {
      const extPath = path.join(basePath, id);
      try {
        const stats = await fs.stat(extPath);
        if (stats.isDirectory()) {
          detected.push({ browser, name, id, path: extPath });
        }
      } catch {
        // Not found
      }
    }
  }

  return detected;
}
