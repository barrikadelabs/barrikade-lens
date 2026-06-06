import { exec } from 'node:child_process';
import os from 'node:os';

// Active AI processes signatures
const AI_PROCESS_SIGNATURES = [
  { signature: 'cursor', label: 'Cursor IDE Agent' },
  { signature: 'windsurf', label: 'Windsurf IDE Agent' },
  { signature: 'cline', label: 'Cline Agent client' },
  { signature: 'roo-code', label: 'Roo Code Agent client' },
  { signature: 'roo', label: 'Roo Code Agent' },
  { signature: 'claude', label: 'Claude Desktop Agent' },
  { signature: 'claude-code', label: 'Claude Code CLI Agent' },
  { signature: 'codex', label: 'OpenAI Codex CLI Agent' },
  { signature: 'gemini', label: 'Antigravity CLI Agent' },
  { signature: 'antigravity', label: 'Antigravity Agent' },
  { signature: 'aider', label: 'Aider CLI Agent' },
  { signature: 'opencode', label: 'OpenCode CLI Agent' },
  { signature: 'openclaw', label: 'OpenClaw Agent' },
  { signature: 'kiro', label: 'Kiro IDE Agent' },
  { signature: 'goose', label: 'Goose CLI Agent' },
  { signature: 'warp', label: 'Warp Terminal AI' },
  { signature: 'amazon-q', label: 'Amazon Q Developer' },
  { signature: 'amazonq', label: 'Amazon Q Developer' },
  { signature: 'copilot', label: 'GitHub Copilot Agent' },
  { signature: 'augment', label: 'Augment Code Agent' },
  { signature: 'junie', label: 'JetBrains Junie Agent' },
  { signature: 'qodo', label: 'Qodo Gen Agent' },
  { signature: 'continue', label: 'Continue.dev Extension' },
  { signature: 'ollama', label: 'Ollama LLM Daemon' },
  { signature: 'lmstudio', label: 'LM Studio Inference Server' },
  { signature: 'lm-studio', label: 'LM Studio Inference Server' },
  { signature: 'jan', label: 'Jan.ai local LLM' },
  { signature: 'llama', label: 'Llama.cpp model runtime' },
  { signature: 'vllm', label: 'vLLM model runtime' },
  { signature: 'anythingllm', label: 'AnythingLLM client' },
  { signature: 'trae', label: 'Trae IDE Agent' },
  { signature: 'zed', label: 'Zed Editor AI client' }
];

const RUNTIME_SIGNATURES = [
  { signature: 'node', label: 'Node.js runtime' },
  { signature: 'python', label: 'Python runtime' },
  { signature: 'bun', label: 'Bun runtime' },
  { signature: 'uv', label: 'uv package manager' }
];

/**
 * Runs a shell command and returns stdout.
 * 
 * @param {string} cmd 
 * @returns {Promise<string>}
 */
function execPromise(cmd) {
  return new Promise((resolve) => {
    exec(cmd, (error, stdout) => {
      resolve(stdout || '');
    });
  });
}

/**
 * Sweeps running system processes for AI agents, model servers, and runtimes.
 * 
 * @returns {Promise<{
 *   aiProcesses: Array<{ name: string, label: string }>,
 *   runtimes: Array<{ name: string, label: string }>
 * }>}
 */
export async function scanProcesses() {
  const platform = os.platform();
  let cmd = '';

  if (platform === 'win32') {
    cmd = 'tasklist /FO CSV';
  } else {
    // macOS and Linux
    cmd = 'ps -ax -o comm';
  }

  const rawOutput = await execPromise(cmd);
  const processLines = rawOutput.toLowerCase().split('\n');

  const aiProcesses = [];
  const runtimes = [];

  // Match AI processes
  for (const sig of AI_PROCESS_SIGNATURES) {
    const found = processLines.some(line => line.includes(sig.signature));
    if (found) {
      aiProcesses.push({ name: sig.signature, label: sig.label });
    }
  }

  // Match standard runtimes
  for (const sig of RUNTIME_SIGNATURES) {
    const found = processLines.some(line => line.includes(sig.signature));
    if (found) {
      runtimes.push({ name: sig.signature, label: sig.label });
    }
  }

  return { aiProcesses, runtimes };
}
