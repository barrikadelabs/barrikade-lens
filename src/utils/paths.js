import os from 'node:os';
import path from 'node:path';

/**
 * Resolves the configuration paths for all supported AI coding tools and clients
 * based on the current operating system and working directory.
 * 
 * @param {string} [cwd=process.cwd()] The workspace directory to check project-level files in
 * @returns {Array<{ tool: string, path: string, scope: 'global' | 'project', type: string }>}
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

  // 6. VS Code Native & Settings
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

  // 7. Continue Extension
  addPath('Continue (Global)', path.join(home, '.continue', 'config.json'), 'global', 'continue');
  addPath('Continue (Project)', path.join(cwd, '.continue', 'config.json'), 'project', 'continue');

  // 8. Zed Editor Settings
  if (platform === 'darwin') {
    addPath('Zed Settings', path.join(home, '.config', 'zed', 'settings.json'), 'global', 'zed');
  } else if (platform === 'win32') {
    addPath('Zed Settings', path.join(home, '.config', 'zed', 'settings.json'), 'global', 'zed'); // Zed uses ~/.config/zed on windows too
  } else {
    addPath('Zed Settings', path.join(home, '.config', 'zed', 'settings.json'), 'global', 'zed');
  }

  // 9. Antigravity SDK
  addPath('Antigravity (Global JSON)', path.join(home, '.antigravity.json'), 'global', 'antigravity');
  addPath('Antigravity Settings (User)', path.join(home, '.antigravity', 'antigravity'), 'global', 'antigravity');
  addPath('Antigravity IDE (User)', path.join(home, '.antigravity-ide', 'antigravity-ide'), 'global', 'antigravity');
  addPath('Antigravity (Global config.json)', path.join(home, '.antigravity', 'config.json'), 'global', 'antigravity');
  addPath('Antigravity (Global Config)', path.join(home, '.config', 'antigravity', 'config.json'), 'global', 'antigravity');
  addPath('Antigravity Config (User)', path.join(home, '.gemini', 'antigravity', 'config.json'), 'global', 'antigravity');
  addPath('Antigravity Settings (User AppData)', path.join(home, '.gemini', 'antigravity', 'settings.json'), 'global', 'antigravity');
  addPath('Gemini Config (User)', path.join(home, '.gemini', 'config.json'), 'global', 'antigravity');
  addPath('Gemini Settings (User)', path.join(home, '.gemini', 'settings.json'), 'global', 'antigravity');
  addPath('Antigravity (Project JSON)', path.join(cwd, 'antigravity.json'), 'project', 'antigravity');
  addPath('Antigravity (Project .JSON)', path.join(cwd, '.antigravity.json'), 'project', 'antigravity');
  addPath('Antigravity (Project Config)', path.join(cwd, '.antigravity', 'config.json'), 'project', 'antigravity');

  // 10. OpenCode CLI
  addPath('OpenCode (User)', path.join(home, '.opencode.json'), 'global', 'antigravity');
  addPath('OpenCode Config (User)', path.join(home, '.opencode', 'config.json'), 'global', 'antigravity');
  addPath('OpenCode System Config (User)', path.join(home, '.config', 'opencode', 'config.json'), 'global', 'antigravity');
  addPath('OpenCode (Project)', path.join(cwd, '.opencode.json'), 'project', 'antigravity');
  addPath('OpenCode Config (Project)', path.join(cwd, '.opencode', 'config.json'), 'project', 'antigravity');

  // 11. Codex CLI
  addPath('Codex CLI (User)', path.join(home, '.codex.json'), 'global', 'antigravity');
  addPath('Codex CLI Config (User)', path.join(home, '.codex', 'config.json'), 'global', 'antigravity');
  addPath('Codex CLI Global State (User)', path.join(home, '.codex', '.codex-global-state.json'), 'global', 'antigravity');
  addPath('Codex CLI (Project)', path.join(cwd, '.codex.json'), 'project', 'antigravity');
  addPath('Codex CLI Config (Project)', path.join(cwd, '.codex', 'config.json'), 'project', 'antigravity');

  // 12. OpenClaw CLI
  addPath('OpenClaw (User)', path.join(home, '.openclaw', 'openclaw.json'), 'global', 'antigravity');
  addPath('OpenClaw System Config (User)', path.join(home, '.config', 'openclaw', 'config.json'), 'global', 'antigravity');
  addPath('OpenClaw (Project)', path.join(cwd, '.openclaw', 'openclaw.json'), 'project', 'antigravity');

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

  const dirs = [
    // Global State Dirs
    { name: 'Cursor IDE', path: path.join(home, '.cursor'), scope: 'global' },
    { name: 'Cline', path: path.join(home, '.cline'), scope: 'global' },
    { name: 'Roo Code', path: path.join(home, '.roo'), scope: 'global' },
    { name: 'Continue', path: path.join(home, '.continue'), scope: 'global' },
    { name: 'Windsurf / Codeium', path: path.join(home, '.codeium'), scope: 'global' },
    { name: 'Claude Code', path: path.join(home, '.claude'), scope: 'global' },
    { name: 'Codex CLI', path: path.join(home, '.codex'), scope: 'global' },
    { name: 'Antigravity / Gemini', path: path.join(home, '.gemini'), scope: 'global' },
    { name: 'Antigravity App Data', path: path.join(home, '.gemini', 'antigravity'), scope: 'global' },
    { name: 'OpenCode', path: path.join(home, '.opencode'), scope: 'global' },
    { name: 'JetBrains IDE Option Folder', path: jbBase, scope: 'global' },
    
    // Project State Dirs
    { name: 'Cursor State', path: path.join(cwd, '.cursor'), scope: 'project' },
    { name: 'Roo Code State', path: path.join(cwd, '.roo'), scope: 'project' },
    { name: 'Cline State', path: path.join(cwd, '.cline'), scope: 'project' },
    { name: 'Continue State', path: path.join(cwd, '.continue'), scope: 'project' },
    { name: 'Aider State', path: path.join(cwd, '.aider'), scope: 'project' },
    { name: 'Claude Code State', path: path.join(cwd, '.claude'), scope: 'project' },
    { name: 'OpenCode Project', path: path.join(cwd, '.opencode'), scope: 'project' }
  ];

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
    { name: '.clinerules', path: path.join(cwd, '.clinerules') },
    { name: '.windsurfrules', path: path.join(cwd, '.windsurfrules') },
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
