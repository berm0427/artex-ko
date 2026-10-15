// Run Linux REA over stdio from a Windows MCP client. Only tools/call argument
// values that are Windows absolute paths are rewritten for WSL's /mnt mounts.
import { spawn } from 'node:child_process';

export function wslPath(value) {
  if (typeof value !== 'string' || !/^[A-Za-z]:[\\/]/.test(value)) return value;
  const drive = value[0].toLowerCase();
  return `/mnt/${drive}/${value.slice(3).replaceAll('\\', '/')}`;
}

function mapPaths(value) {
  if (Array.isArray(value)) return value.map(mapPaths);
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, mapPaths(item)]));
  }
  return wslPath(value);
}

export function forwardLine(line) {
  try {
    const message = JSON.parse(line);
    if (message.method !== 'tools/call' || !message.params?.arguments) return line;
    message.params.arguments = mapPaths(message.params.arguments);
    return JSON.stringify(message);
  } catch {
    return line;
  }
}

if (process.argv[2] === '--self-test') {
  const input = JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: {
    name: 'open_binary', arguments: { path: 'C:\\Users\\test\\sample.elf' },
  } });
  if (JSON.parse(forwardLine(input)).params.arguments.path !== '/mnt/c/Users/test/sample.elf') {
    throw new Error('Windows-to-WSL path conversion failed');
  }
  process.stdout.write('bridge path conversion OK\n');
} else {
  const [distro, entry, ghidraDir, linuxNode = '/usr/bin/node'] = process.argv.slice(2);
  if (!distro || !entry?.startsWith('/') || !ghidraDir?.startsWith('/') || !linuxNode.startsWith('/')) {
    process.stderr.write('Usage: node rea-wsl-mcp-bridge.mjs <distro> <Linux rea.mjs> <Linux Ghidra directory> [Linux node]\n');
    process.exitCode = 2;
  } else {
    const child = spawn('wsl.exe', [
      '-d', distro, '--cd', '/', '--', 'env',
      `GHIDRA_INSTALL_DIR=${ghidraDir}`, 'REA_ANALYSIS_PROVIDER=ghidra',
      linuxNode, entry, 'mcp',
    ], { stdio: ['pipe', 'pipe', 'inherit'], windowsHide: true });
    child.stdout.pipe(process.stdout);
    child.on('error', (err) => { process.stderr.write(`${err.message}\n`); process.exitCode = 1; });
    child.on('close', (code) => { process.exitCode = code ?? 1; });
    process.on('SIGINT', () => child.kill('SIGINT'));
    process.on('SIGTERM', () => child.kill('SIGTERM'));

    process.stdin.setEncoding('utf8');
    let pending = '';
    process.stdin.on('data', (chunk) => {
      pending += chunk;
      let end;
      while ((end = pending.indexOf('\n')) !== -1) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 1);
        child.stdin.write(`${forwardLine(line)}\n`);
      }
    });
    process.stdin.on('end', () => {
      if (pending) child.stdin.write(forwardLine(pending));
      child.stdin.end();
    });
  }
}
