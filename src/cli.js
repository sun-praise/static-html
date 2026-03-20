const path = require("node:path");
const { createServer } = require("./server");

const DEFAULT_SERVER_URL = "http://127.0.0.1:3939";
const DEFAULT_HOST = "127.0.0.1";
const DEFAULT_PORT = 3939;

function printUsage() {
  console.log(`Usage:
  html-server start [--host 127.0.0.1] [--port 3939]
  html-server send <file.html> [--server http://127.0.0.1:3939]`);
}

function parseFlags(args) {
  const flags = {};
  const positionals = [];

  for (let index = 0; index < args.length; index += 1) {
    const token = args[index];

    if (!token.startsWith("--")) {
      positionals.push(token);
      continue;
    }

    const name = token.slice(2);
    const value = args[index + 1];

    if (!value || value.startsWith("--")) {
      throw new Error(`Missing value for --${name}`);
    }

    flags[name] = value;
    index += 1;
  }

  return { flags, positionals };
}

function validateHtmlPath(filePath) {
  if (!filePath) {
    throw new Error("Missing HTML file path.");
  }

  const resolvedPath = path.resolve(filePath);
  const extension = path.extname(resolvedPath).toLowerCase();

  if (extension !== ".html" && extension !== ".htm") {
    throw new Error("Only .html and .htm files are supported.");
  }

  return resolvedPath;
}

async function startCommand(args) {
  const { flags } = parseFlags(args);
  const host = flags.host || DEFAULT_HOST;
  const port = flags.port ? Number.parseInt(flags.port, 10) : DEFAULT_PORT;

  if (!Number.isInteger(port) || port <= 0) {
    throw new Error("Port must be a positive integer.");
  }

  const server = createServer({ host, port });
  await server.start();

  console.log(`HTML server listening on http://${host}:${port}`);

  const shutdown = async () => {
    await server.stop();
    process.exit(0);
  };

  process.once("SIGINT", shutdown);
  process.once("SIGTERM", shutdown);
}

async function sendCommand(args) {
  const { flags, positionals } = parseFlags(args);
  const entryFile = validateHtmlPath(positionals[0]);
  const serverUrl = new URL(flags.server || DEFAULT_SERVER_URL);
  const endpoint = new URL("/api/sessions", serverUrl);

  let response;

  try {
    response = await fetch(endpoint, {
      method: "POST",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify({ filePath: entryFile }),
    });
  } catch (error) {
    if (error.cause && error.cause.code === "ECONNREFUSED") {
      throw new Error(
        `Could not reach ${serverUrl.origin}. Start the server with "html-server start" first.`,
      );
    }

    throw new Error(`Failed to reach server: ${error.message}`);
  }

  let payload;

  try {
    payload = await response.json();
  } catch (error) {
    throw new Error("Server returned an invalid response.");
  }

  if (!response.ok) {
    throw new Error(payload.error || "Server rejected the request.");
  }

  console.log(payload.url);
}

async function main(args = process.argv.slice(2)) {
  const [command, ...rest] = args;

  if (!command || command === "--help" || command === "-h") {
    printUsage();
    return;
  }

  if (command === "start") {
    await startCommand(rest);
    return;
  }

  if (command === "send") {
    await sendCommand(rest);
    return;
  }

  throw new Error(`Unknown command: ${command}`);
}

module.exports = {
  DEFAULT_HOST,
  DEFAULT_PORT,
  DEFAULT_SERVER_URL,
  main,
  parseFlags,
  sendCommand,
  startCommand,
  validateHtmlPath,
};
