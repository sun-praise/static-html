const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");
const { once } = require("node:events");
const { spawn } = require("node:child_process");
const { createServer } = require("../src/server");

const fixtureHtml = path.resolve(__dirname, "../fixtures/basic/index.html");

test("POST /api/sessions creates a session and serves HTML plus assets", async () => {
  const server = createServer({ host: "127.0.0.1", port: 0 });
  await server.start();
  const origin = server.getOrigin();

  try {
    const createResponse = await fetch(`${origin}/api/sessions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ filePath: fixtureHtml }),
    });

    assert.equal(createResponse.status, 201);

    const session = await createResponse.json();
    assert.match(session.url, /\/s\/.+\/$/);

    const htmlResponse = await fetch(session.url);
    const html = await htmlResponse.text();

    assert.equal(htmlResponse.status, 200);
    assert.match(html, /Static HTML Preview/);

    const cssResponse = await fetch(new URL("style.css", session.url));
    const css = await cssResponse.text();

    assert.equal(cssResponse.status, 200);
    assert.match(css, /background/);
  } finally {
    await server.stop();
  }
});

test("resource traversal is rejected", async () => {
  const server = createServer({ host: "127.0.0.1", port: 0 });
  await server.start();
  const origin = server.getOrigin();

  try {
    const createResponse = await fetch(`${origin}/api/sessions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ filePath: fixtureHtml }),
    });
    const session = await createResponse.json();

    const traversalResponse = await fetch(`${session.url}..%2Fpackage.json`);
    assert.equal(traversalResponse.status, 403);
  } finally {
    await server.stop();
  }
});

test("CLI send prints the created session URL", async () => {
  const server = createServer({ host: "127.0.0.1", port: 0 });
  await server.start();
  const origin = server.getOrigin();

  try {
    const child = spawn(
      process.execPath,
      [
        path.resolve(__dirname, "../bin/html-server.js"),
        "send",
        fixtureHtml,
        "--server",
        origin,
      ],
      { stdio: ["ignore", "pipe", "pipe"] },
    );

    const stdout = [];
    const stderr = [];

    child.stdout.on("data", (chunk) => stdout.push(chunk));
    child.stderr.on("data", (chunk) => stderr.push(chunk));

    const [code] = await once(child, "close");

    assert.equal(code, 0, Buffer.concat(stderr).toString("utf8"));
    const output = Buffer.concat(stdout).toString("utf8");
    assert.match(output, /\/s\/.+\/\n/);
    assert.match(output, new RegExp(origin.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
  } finally {
    await server.stop();
  }
});

test("CLI send fails clearly when the server is unavailable", async () => {
  const child = spawn(
    process.execPath,
    [
      path.resolve(__dirname, "../bin/html-server.js"),
      "send",
      fixtureHtml,
      "--server",
      "http://127.0.0.1:4399",
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );

  const stderr = [];
  child.stderr.on("data", (chunk) => stderr.push(chunk));

  const [code] = await once(child, "close");

  assert.equal(code, 1);
  assert.match(
    Buffer.concat(stderr).toString("utf8"),
    /Start the server with "html-server start" first\./,
  );
});
