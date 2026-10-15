// Launch a globally installed REA MCP server from its package directory.
// REA's own MCP doctor uses this cwd; starting from ARTEX's repository can
// make provider discovery stall on Windows.
import { dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

if (!process.argv[2]) {
  process.stderr.write('Usage: node rea-mcp-launch.mjs <absolute path to rea.mjs>\n');
  process.exitCode = 2;
} else {
  const entry = resolve(process.argv[2]);
  process.chdir(resolve(dirname(entry), '..'));
  process.argv = [process.execPath, entry, 'mcp'];
  await import(pathToFileURL(entry).href);
}
