import os from 'node:os';
import path from 'node:path';

/**
 * @typedef {{
 * tool: string,
 * path: string,
 * scope: 'global' | 'project',
 * type: string
 * }} ScanTarget
 */


/**
 * Resolves the configuration paths for all supported AI coding tools and clients
 * based on the current operating system and working directory.
 * 
 * @param {string} [cwd=process.cwd()] The workspace directory to check project-level files in
 * @returns {ScanTarget[]} List of configuration paths to audit
 */
export function getScanPaths(cwd = process.cwd()) {
  const home = os.homedir();
  const platform = os.platform();
  
  let appData = '';
  if (platform === 'win32') {
    appData = process.env.APPDATA || path.join(home, 'AppData', 'Roaming');
  }

  const paths = [];
  const addPath = (tool, resolvedPath, scope, type) => {
    paths.push({ tool, path: resolvedPath, scope, type });
  };

  // 1. Claude Desktop
  if (platform === 'darwin') {
    addPath('Claude Desktop', path.join(home, 'Library', 'Application Support', 'Claude', 'claude_desktop_config.json'), 'global', 'mcpServers');
  } else if (platform === 'win32') {
    addPath('Claude Desktop', path.join(appData, 'Claude', 'claude_desktop_config.json'), 'global', 'mcpServers');
  } else {
    addPath('Claude Desktop', path.join(home, '.config', 'Claude', 'claude_desktop_config.json'), 'global', 'mcpServers');
  }

  // 2. Cursor IDE
  addPath('Cursor (Global)', path.join(home, '.cursor', 'mcp.json'), 'global', 'mcpServers');
  addPath('Cursor (Project)', path.join(cwd, '.cursor', 'mcp.json'), 'project', 'mcpServers');

  // 3. Claude Code
  addPath('Claude Code (User)', path.join(home, '.claude.json'), 'global', 'mcpServers');
  addPath('Claude Code (Project)', path.join(cwd, '.mcp.json'), 'project', 'mcpServers');

  // 4. Cline (VS Code Extension)
  const clineFilename = 'cline_mcp_settings.json';
  if (platform === 'darwin') {
    addPath('Cline (VS Code)', path.join(home, 'Library', 'Application Support', 'Code', 'User', 'globalStorage', 'saoudrizwan.claude-dev', 'settings', clineFilename), 'global', 'mcpServers');
    addPath('Cline (VS Code Insiders)', path.join(home, 'Library', 'Application Support', 'Code - Insiders', 'User', 'globalStorage', 'saoudrizwan.claude-dev', 'settings', clineFilename), 'global', 'mcpServers');
  } else if (platform === 'win32') {
    addPath('Cline (VS Code)', path.join(appData, 'Code', 'User', 'globalStorage', 'saoudrizwan.claude-dev', 'settings', clineFilename), 'global', 'mcpServers');
    addPath('Cline (VS Code Insiders)', path.join(appData, 'Code - Insiders', 'User', 'globalStorage', 'saoudrizwan.claude-dev', 'settings', clineFilename), 'global', 'mcpServers');
  } else {
    addPath('Cline (VS Code)', path.join(home, '.config', 'Code', 'User', 'globalStorage', 'saoudrizwan.claude-dev', 'settings', clineFilename), 'global', 'mcpServers');
    addPath('Cline (VS Code Insiders)', path.join(home, '.config', 'Code - Insiders', 'User', 'globalStorage', 'saoudrizwan.claude-dev', 'settings', clineFilename), 'global', 'mcpServers');
  }
  addPath('Cline (Global)', path.join(home, '.cline', 'data', 'settings', clineFilename), 'global', 'mcpServers');

  // 5. Windsurf
  addPath('Windsurf', path.join(home, '.codeium', 'windsurf', 'mcp_config.json'), 'global', 'mcpServers');

  // 6. Zed Editor
  addPath('Zed Settings (Global)', path.join(home, '.config', 'zed', 'settings.json'), 'global', 'zed');
  addPath('Zed Settings (Project)', path.join(cwd, '.zed', 'settings.json'), 'project', 'zed');
  if (platform === 'win32') {
    addPath('Zed Settings (Global AppData)', path.join(appData, 'Zed', 'settings.json'), 'global', 'zed');
  }

  // 7. Roo Code (VS Code Extension)
  if (platform === 'darwin') {
    addPath('Roo Code (VS Code)', path.join(home, 'Library', 'Application Support', 'Code', 'User', 'globalStorage', 'rooveterinaryinc.roo-cline', 'settings', clineFilename), 'global', 'mcpServers');
    addPath('Roo Code (VS Code Insiders)', path.join(home, 'Library', 'Application Support', 'Code - Insiders', 'User', 'globalStorage', 'rooveterinaryinc.roo-cline', 'settings', clineFilename), 'global', 'mcpServers');
  } else if (platform === 'win32') {
    addPath('Roo Code (VS Code)', path.join(appData, 'Code', 'User', 'globalStorage', 'rooveterinaryinc.roo-cline', 'settings', clineFilename), 'global', 'mcpServers');
    addPath('Roo Code (VS Code Insiders)', path.join(appData, 'Code - Insiders', 'User', 'globalStorage', 'rooveterinaryinc.roo-cline', 'settings', clineFilename), 'global', 'mcpServers');
  } else {
    addPath('Roo Code (VS Code)', path.join(home, '.config', 'Code', 'User', 'globalStorage', 'rooveterinaryinc.roo-cline', 'settings', clineFilename), 'global', 'mcpServers');
    addPath('Roo Code (VS Code Insiders)', path.join(home, '.config', 'Code - Insiders', 'User', 'globalStorage', 'rooveterinaryinc.roo-cline', 'settings', clineFilename), 'global', 'mcpServers');
  }
  addPath('Roo Code (Project)', path.join(cwd, '.roo', 'mcp.json'), 'project', 'mcpServers');

  // 8. GitHub Copilot
  addPath('GitHub Copilot (Project)', path.join(cwd, '.github', 'mcp.json'), 'project', 'mcpServers');
  addPath('GitHub Copilot CLI (Global)', path.join(home, '.copilot', 'mcp-config.json'), 'global', 'mcpServers');
  addPath('GitHub Copilot CLI (Project)', path.join(cwd, '.copilot', 'mcp-config.json'), 'project', 'mcpServers');
  addPath('GitHub Copilot (JetBrains)', path.join(home, '.config', 'github-copilot', 'intellij', 'mcp.json'), 'global', 'mcpServers');

  // 9. Kiro IDE
  addPath('Kiro IDE (Global)', path.join(home, '.kiro', 'settings', 'mcp.json'), 'global', 'mcpServers');
  addPath('Kiro IDE (Project)', path.join(cwd, '.kiro', 'settings', 'mcp.json'), 'project', 'mcpServers');

  // 10. Amazon Q Developer
  addPath('Amazon Q (Global MCP)', path.join(home, '.aws', 'amazonq', 'mcp.json'), 'global', 'mcpServers');
  addPath('Amazon Q (Global Default)', path.join(home, '.aws', 'amazonq', 'default.json'), 'global', 'amazonq');
  addPath('Amazon Q (Project MCP)', path.join(cwd, '.amazonq', 'mcp.json'), 'project', 'mcpServers');
  addPath('Amazon Q (Project Default)', path.join(cwd, '.amazonq', 'default.json'), 'project', 'amazonq');

  // 11. Warp Terminal
  addPath('Warp Terminal (Global)', path.join(home, '.warp', '.mcp.json'), 'global', 'mcpServers');

  // 12. Goose
  addPath('Goose (Global config.yaml)', path.join(home, '.config', 'goose', 'config.yaml'), 'global', 'yaml');
  if (platform === 'win32') {
    addPath('Goose (Global AppData)', path.join(appData, 'Block', 'goose', 'config', 'config.yaml'), 'global', 'yaml');
  } else if (platform === 'darwin') {
    addPath('Goose (Global App Support)', path.join(home, 'Library', 'Application Support', 'Block', 'goose', 'config', 'config.yaml'), 'global', 'yaml');
  }

  // 13. Aider
  addPath('Aider (Global)', path.join(home, '.aider.conf.yml'), 'global', 'yaml');
  addPath('Aider (Project)', path.join(cwd, '.aider.conf.yml'), 'project', 'yaml');

  // 14. JetBrains Junie
  addPath('JetBrains Junie (Global)', path.join(home, '.junie', 'mcp', 'mcp.json'), 'global', 'mcpServers');
  addPath('JetBrains Junie (Project)', path.join(cwd, '.junie', 'mcp', 'mcp.json'), 'project', 'mcpServers');

  // 15. VS Code Native & Settings
  addPath('VS Code (Project Native MCP)', path.join(cwd, '.vscode', 'mcp.json'), 'project', 'vscodeServers');
  addPath('VS Code Settings (Project)', path.join(cwd, '.vscode', 'settings.json'), 'project', 'vscodeSettings');
  
  if (platform === 'darwin') {
    addPath('VS Code (Global Native MCP)', path.join(home, 'Library', 'Application Support', 'Code', 'User', 'mcp.json'), 'global', 'vscodeServers');
    addPath('VS Code (Global Insiders Native MCP)', path.join(home, 'Library', 'Application Support', 'Code - Insiders', 'User', 'mcp.json'), 'global', 'vscodeServers');
    addPath('VS Code Settings (Global)', path.join(home, 'Library', 'Application Support', 'Code', 'User', 'settings.json'), 'global', 'vscodeSettings');
  } else if (platform === 'win32') {
    addPath('VS Code (Global Native MCP)', path.join(appData, 'Code', 'User', 'mcp.json'), 'global', 'vscodeServers');
    addPath('VS Code (Global Insiders Native MCP)', path.join(appData, 'Code - Insiders', 'User', 'mcp.json'), 'global', 'vscodeServers');
    addPath('VS Code Settings (Global)', path.join(appData, 'Code', 'User', 'settings.json'), 'global', 'vscodeSettings');
  } else {
    addPath('VS Code (Global Native MCP)', path.join(home, '.config', 'Code', 'User', 'mcp.json'), 'global', 'vscodeServers');
    addPath('VS Code (Global Insiders Native MCP)', path.join(home, '.config', 'Code - Insiders', 'User', 'mcp.json'), 'global', 'vscodeServers');
    addPath('VS Code Settings (Global)', path.join(home, '.config', 'Code', 'User', 'settings.json'), 'global', 'vscodeSettings');
  }

  // 16. Continue Extension
  addPath('Continue (Global JSON)', path.join(home, '.continue', 'config.json'), 'global', 'continue');
  addPath('Continue (Project JSON)', path.join(cwd, '.continue', 'config.json'), 'project', 'continue');
  addPath('Continue (Global YAML)', path.join(home, '.continue', 'config.yaml'), 'global', 'yaml');
  addPath('Continue (Project YAML)', path.join(cwd, '.continue', 'config.yaml'), 'project', 'yaml');

  // 17. Antigravity CLI & SDK (underlying folder is .gemini)
  // Gemini CLI becomes Antigravity CLI!
  addPath('Antigravity CLI Config (User)', path.join(home, '.gemini', 'config.json'), 'global', 'antigravityCli');
  addPath('Antigravity CLI Settings (User)', path.join(home, '.gemini', 'settings.json'), 'global', 'antigravityCli');
  addPath('Antigravity SDK Config (User)', path.join(home, '.gemini', 'antigravity', 'config.json'), 'global', 'antigravity');
  addPath('Antigravity SDK Settings (User)', path.join(home, '.gemini', 'antigravity', 'settings.json'), 'global', 'antigravity');
  addPath('Antigravity CLI Config (Project)', path.join(cwd, '.gemini', 'config.json'), 'project', 'antigravityCli');
  addPath('Antigravity CLI Settings (Project)', path.join(cwd, '.gemini', 'settings.json'), 'project', 'antigravityCli');
  addPath('Antigravity SDK Config (Project)', path.join(cwd, '.gemini', 'antigravity', 'config.json'), 'project', 'antigravity');
  addPath('Antigravity SDK Settings (Project)', path.join(cwd, '.gemini', 'antigravity', 'settings.json'), 'project', 'antigravity');

  // 18. OpenCode CLI (opencode.json, opencode.jsonc)
  addPath('OpenCode (User JSON)', path.join(home, '.opencode', 'opencode.json'), 'global', 'opencode');
  addPath('OpenCode (User JSONC)', path.join(home, '.opencode', 'opencode.jsonc'), 'global', 'jsonc');
  addPath('OpenCode (Project JSON)', path.join(cwd, '.opencode', 'opencode.json'), 'project', 'opencode');
  addPath('OpenCode (Project JSONC)', path.join(cwd, '.opencode', 'opencode.jsonc'), 'project', 'jsonc');
  addPath('OpenCode Root (Project JSON)', path.join(cwd, 'opencode.json'), 'project', 'opencode');
  addPath('OpenCode Root (Project JSONC)', path.join(cwd, 'opencode.jsonc'), 'project', 'jsonc');

  // 19. Codex CLI
  addPath('Codex CLI (User TOML)', path.join(home, '.codex', 'config.toml'), 'global', 'toml');
  addPath('Codex CLI Global State (User)', path.join(home, '.codex', '.codex-global-state.json'), 'global', 'codex');
  addPath('Codex CLI (Project TOML)', path.join(cwd, '.codex', 'config.toml'), 'project', 'toml');

  // 20. OpenClaw CLI
  addPath('OpenClaw (User)', path.join(home, '.openclaw', 'openclaw.json'), 'global', 'openclaw');
  addPath('OpenClaw (Project)', path.join(cwd, '.openclaw', 'openclaw.json'), 'project', 'openclaw');

  return paths;
}

/**
 * Returns a list of agent state and logs directories to check for existence.
 * 
 * @param {string} [cwd=process.cwd()] 
 * @returns {Array<{ name: string, path: string, scope: 'global' | 'project' }>}
 */
export function getAgentStateDirs(cwd = process.cwd()) {
  const home = os.homedir();
  const platform = os.platform();
  let jbBase = '';
  
  if (platform === 'darwin') jbBase = path.join(home, 'Library', 'Application Support', 'JetBrains');
  else if (platform === 'win32') jbBase = path.join(process.env.APPDATA || path.join(home, 'AppData', 'Roaming'), 'JetBrains');
  else jbBase = path.join(home, '.config', 'JetBrains');

  /** @type {Array<{ name: string, path: string, scope: 'global' | 'project' }>} */
  const dirs = [
    // Global State Dirs
    { name: 'Cursor IDE', path: path.join(home, '.cursor'), scope: 'global' },
    { name: 'Cline', path: path.join(home, '.cline'), scope: 'global' },
    { name: 'Roo Code', path: path.join(home, '.roo'), scope: 'global' },
    { name: 'Continue', path: path.join(home, '.continue'), scope: 'global' },
    { name: 'Windsurf / Codeium', path: path.join(home, '.codeium'), scope: 'global' },
    { name: 'Claude Code', path: path.join(home, '.claude'), scope: 'global' },
    { name: 'Codex CLI', path: path.join(home, '.codex'), scope: 'global' },
    { name: 'Antigravity CLI / SDK', path: path.join(home, '.gemini'), scope: 'global' },
    { name: 'OpenCode', path: path.join(home, '.opencode'), scope: 'global' },
    { name: 'GitHub Copilot CLI', path: path.join(home, '.copilot'), scope: 'global' },
    { name: 'Kiro IDE', path: path.join(home, '.kiro'), scope: 'global' },
    { name: 'Amazon Q Developer', path: path.join(home, '.aws', 'amazonq'), scope: 'global' },
    { name: 'Warp Terminal', path: path.join(home, '.warp'), scope: 'global' },
    { name: 'Goose', path: path.join(home, '.config', 'goose'), scope: 'global' },
    { name: 'JetBrains Junie', path: path.join(home, '.junie'), scope: 'global' },
    { name: 'JetBrains IDE Option Folder', path: jbBase, scope: 'global' },
    { name: 'Qodo Gen (VS Code)', path: path.join(home, 'Library', 'Application Support', 'Code', 'User', 'globalStorage', 'qodo.qodo-gen'), scope: 'global' },
    
    // Project State Dirs
    { name: 'Cursor State', path: path.join(cwd, '.cursor'), scope: 'project' },
    { name: 'Roo Code State', path: path.join(cwd, '.roo'), scope: 'project' },
    { name: 'Cline State', path: path.join(cwd, '.cline'), scope: 'project' },
    { name: 'Continue State', path: path.join(cwd, '.continue'), scope: 'project' },
    { name: 'Aider State', path: path.join(cwd, '.aider'), scope: 'project' },
    { name: 'Claude Code State', path: path.join(cwd, '.claude'), scope: 'project' },
    { name: 'OpenCode Project', path: path.join(cwd, '.opencode'), scope: 'project' },
    { name: 'Kiro IDE Project', path: path.join(cwd, '.kiro'), scope: 'project' },
    { name: 'Amazon Q Project', path: path.join(cwd, '.amazonq'), scope: 'project' },
    { name: 'GitHub Copilot CLI Project', path: path.join(cwd, '.copilot'), scope: 'project' },
    { name: 'JetBrains Junie Project', path: path.join(cwd, '.junie'), scope: 'project' },
    { name: 'GitHub Config Folder', path: path.join(cwd, '.github'), scope: 'project' },
    { name: 'Zed Project Config', path: path.join(cwd, '.zed'), scope: 'project' },
    { name: 'Codex Project Config', path: path.join(cwd, '.codex'), scope: 'project' },
    { name: 'Antigravity CLI/SDK Project Config', path: path.join(cwd, '.gemini'), scope: 'project' },
    { name: 'VS Code Project Config', path: path.join(cwd, '.vscode'), scope: 'project' }
  ];

  // Add Windows-specific or alternative global directories if appropriate
  if (platform === 'win32') {
    const appDataRoaming = process.env.APPDATA || path.join(home, 'AppData', 'Roaming');
    dirs.push({ name: 'Qodo Gen (VS Code Windows)', path: path.join(appDataRoaming, 'Code', 'User', 'globalStorage', 'qodo.qodo-gen'), scope: 'global' });
  } else if (platform === 'linux') {
    dirs.push({ name: 'Qodo Gen (VS Code Linux)', path: path.join(home, '.config', 'Code', 'User', 'globalStorage', 'qodo.qodo-gen'), scope: 'global' });
  }

  return dirs;
}

/**
 * Returns a list of agent rule files to scan.
 * 
 * @param {string} [cwd=process.cwd()] 
 * @returns {Array<{ name: string, path: string }>}
 */
export function getAgentRuleFiles(cwd = process.cwd()) {
  return [
    { name: 'AGENTS.md', path: path.join(cwd, 'AGENTS.md') },
    { name: 'CLAUDE.md', path: path.join(cwd, 'CLAUDE.md') },
    { name: 'GEMINI.md', path: path.join(cwd, 'GEMINI.md') },
    { name: 'ANTIGRAVITY.md', path: path.join(cwd, 'ANTIGRAVITY.md') },
    { name: '.clinerules', path: path.join(cwd, '.clinerules') },
    { name: '.windsurfrules', path: path.join(cwd, '.windsurfrules') },
    { name: '.cursorrules', path: path.join(cwd, '.cursorrules') },
    { name: '.roomodes', path: path.join(cwd, '.roomodes') },
    { name: 'Cursor Rules Dir', path: path.join(cwd, '.cursor', 'rules') },
    { name: 'Roo Rules Dir', path: path.join(cwd, '.roo', 'rules') }
  ];
}

/**
 * Returns a list of local model cache directories to check.
 * 
 * @returns {Array<{ name: string, path: string }>}
 */
export function getModelDirs() {
  const home = os.homedir();
  const platform = os.platform();
  
  const dirs = [
    { name: 'Ollama Cache', path: path.join(home, '.ollama') },
    { name: 'HuggingFace Cache', path: path.join(home, '.cache', 'huggingface') },
    { name: 'Torch Cache', path: path.join(home, '.cache', 'torch') }
  ];

  if (platform === 'darwin') {
    dirs.push({ name: 'LM Studio Cache', path: path.join(home, 'Library', 'Application Support', 'LM Studio') });
  } else if (platform === 'win32') {
    dirs.push({ name: 'LM Studio Cache', path: path.join(process.env.APPDATA || path.join(home, 'AppData', 'Roaming'), 'LM Studio') });
  } else {
    dirs.push({ name: 'LM Studio Cache', path: path.join(home, '.config', 'LM Studio') });
  }

  return dirs;
}

/**
 * Returns shell history files to audit.
 * 
 * @returns {string[]}
 */
export function getShellHistories() {
  const home = os.homedir();
  return [
    path.join(home, '.zsh_history'),
    path.join(home, '.bash_history'),
    path.join(home, '.history')
  ];
}
