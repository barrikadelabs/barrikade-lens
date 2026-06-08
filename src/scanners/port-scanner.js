import net from 'node:net';
import os from 'node:os';

// Common ports for local AI inference and MCP servers
export const DEFAULT_AI_PORTS = [
  { port: 11434, service: 'Ollama' },
  { port: 1234, service: 'LM Studio' },
  { port: 1337, service: 'Jan.ai' },
  { port: 8080, service: 'LocalAI / Llama.cpp' },
  { port: 8000, service: 'LocalAI / vLLM / LiteLLM' },
  { port: 7860, service: 'Text Generation WebUI (Gradio)' },
  { port: 5000, service: 'Text Generation WebUI (API)' },
  { port: 3845, service: 'Figma Desktop MCP' },
  { port: 63334, service: 'JetBrains MCP' },
  { port: 64342, service: 'JetBrains MCP (alt)' }
];

/**
 * Gets active non-loopback IPv4 addresses of the host machine
 * @returns {string[]}
 */
export function getLocalLanIps() {
  const interfaces = os.networkInterfaces();
  const ips = [];
  for (const name of Object.keys(interfaces)) {
    for (const netInfo of interfaces[name]) {
      if (netInfo.family === 'IPv4' && !netInfo.internal) {
        ips.push(netInfo.address);
      }
    }
  }
  return ips;
}

/**
 * Tests if a socket can connect to host:port.
 * @param {string} host 
 * @param {number} port 
 * @param {number} timeoutMs 
 * @returns {Promise<boolean>}
 */
function testConnection(host, port, timeoutMs = 300) {
  return new Promise((resolve) => {
    const socket = new net.Socket();
    let resolved = false;

    const timer = setTimeout(() => {
      if (!resolved) {
        resolved = true;
        socket.destroy();
        resolve(false);
      }
    }, timeoutMs);

    socket.on('connect', () => {
      if (!resolved) {
        resolved = true;
        clearTimeout(timer);
        socket.destroy();
        resolve(true);
      }
    });

    socket.on('error', () => {
      if (!resolved) {
        resolved = true;
        clearTimeout(timer);
        socket.destroy();
        resolve(false);
      }
    });

    socket.connect(port, host);
  });
}

/**
 * Given an open port, checks whether it's reachable on any LAN interface.
 * @returns {Promise<boolean>}
 */
async function checkExposure(port, lanIps, timeoutMs) {
  if (lanIps.length === 0) return false;
  // Probe every LAN IP concurrently; exposed if ANY responds.
  const results = await Promise.all(
    lanIps.map(ip => testConnection(ip, port, timeoutMs))
  );
  return results.some(Boolean);
}

/**
 * Scans local AI ports and determines if they are exposed to the LAN.
 * @param {number} [timeoutMs=300] Connection timeout in ms
 * @typedef {{
 *   port: number,
 *   service: string,
 *   open: boolean,
 *   exposed: boolean,
 *   binding: '127.0.0.1' | '0.0.0.0' | 'offline',
 *   risk: 'CRITICAL' | 'INFO' | 'CLEAN'
 * }} PortResult
 */

/**
 * Scans local AI ports and determines if they are exposed to the LAN.
 * @param {number} [timeoutMs=300] Connection timeout in ms
 * @returns {Promise<PortResult[]>}
 */
export async function scanPorts(timeoutMs = 300) {
  const lanIps = getLocalLanIps();

  
  // one promise per port entry. Each resolves to a full result object.
  /** @type {PortResult[]} */
  const results = await Promise.all(
    DEFAULT_AI_PORTS.map(async (entry) => {
      const isOpenLocal = await testConnection('127.0.0.1', entry.port, timeoutMs);

      if (!isOpenLocal) {
        return {
          port: entry.port,
          service: entry.service,
          open: false,
          exposed: false,
          binding: 'offline',
          risk: 'CLEAN'
        };
      }

      const isExposed = await checkExposure(entry.port, lanIps, timeoutMs);
      return {
        port: entry.port,
        service: entry.service,
        open: true,
        exposed: isExposed,
        binding: isExposed ? '0.0.0.0' : '127.0.0.1',
        risk: isExposed ? 'CRITICAL' : 'INFO'
      };
    })
  );

  return results;
}
