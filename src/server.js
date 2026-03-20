const fs = require("node:fs/promises");
const path = require("node:path");
const { randomUUID } = require("node:crypto");
const http = require("node:http");
const express = require("express");

function isHtmlFile(filePath) {
  const extension = path.extname(filePath).toLowerCase();
  return extension === ".html" || extension === ".htm";
}

function isSubpath(rootDir, targetPath) {
  const relativePath = path.relative(rootDir, targetPath);
  return (
    relativePath === "" ||
    (!relativePath.startsWith("..") && !path.isAbsolute(relativePath))
  );
}

async function ensureFile(filePath) {
  const stat = await fs.stat(filePath);

  if (!stat.isFile()) {
    throw new Error("Target path is not a file.");
  }
}

function createSessionStore() {
  const sessions = new Map();

  return {
    create(entryFile) {
      const sessionId = randomUUID();
      const rootDir = path.dirname(entryFile);
      const session = {
        sessionId,
        entryFile,
        rootDir,
        createdAt: new Date().toISOString(),
      };

      sessions.set(sessionId, session);
      return session;
    },
    get(sessionId) {
      return sessions.get(sessionId);
    },
    listRecent(limit = 20) {
      return Array.from(sessions.values())
        .sort((left, right) => right.createdAt.localeCompare(left.createdAt))
        .slice(0, limit);
    },
  };
}

function renderHomePage(sessions) {
  const sessionList = sessions.length
    ? sessions
        .map(
          (session) => `<li>
  <a href="/s/${session.sessionId}/">${path.basename(session.entryFile)}</a>
  <code>${session.entryFile}</code>
  <time datetime="${session.createdAt}">${session.createdAt}</time>
</li>`,
        )
        .join("\n")
    : "<li>No preview sessions yet.</li>";

  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>HTML Preview Server</title>
    <style>
      :root {
        color-scheme: light;
        font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
      }
      body {
        margin: 0;
        padding: 2rem;
        background: linear-gradient(180deg, #f6f4ee 0%, #ebe7dc 100%);
        color: #171717;
      }
      main {
        max-width: 900px;
        margin: 0 auto;
        background: rgba(255, 255, 255, 0.8);
        border: 1px solid #d6d1c6;
        border-radius: 16px;
        padding: 2rem;
        box-shadow: 0 20px 50px rgba(0, 0, 0, 0.08);
      }
      h1 {
        margin-top: 0;
      }
      code {
        display: inline-block;
        margin-left: 0.75rem;
        padding: 0.15rem 0.4rem;
        border-radius: 6px;
        background: #f2efe6;
      }
      ul {
        padding-left: 1.2rem;
      }
      li {
        margin-bottom: 0.8rem;
      }
      time {
        margin-left: 0.75rem;
        color: #5a5a5a;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>HTML Preview Server</h1>
      <p>Register a file with <code>html-server send path/to/file.html</code> and open the returned session URL.</p>
      <ul>${sessionList}</ul>
    </main>
  </body>
</html>`;
}

async function loadHtml(filePath) {
  return fs.readFile(filePath, "utf8");
}

function createApp(options = {}) {
  const store = options.store || createSessionStore();
  const app = express();

  app.disable("x-powered-by");
  app.use(express.json());

  app.get("/", (req, res) => {
    res.type("html").send(renderHomePage(store.listRecent()));
  });

  app.post("/api/sessions", async (req, res) => {
    const filePath = req.body && typeof req.body.filePath === "string" ? req.body.filePath : null;

    if (!filePath) {
      res.status(400).json({ error: "filePath is required." });
      return;
    }

    const entryFile = path.resolve(filePath);

    if (!isHtmlFile(entryFile)) {
      res.status(400).json({ error: "Only .html and .htm files are supported." });
      return;
    }

    try {
      await ensureFile(entryFile);
    } catch (error) {
      if (error && error.code === "ENOENT") {
        res.status(404).json({ error: "HTML file does not exist." });
        return;
      }

      res.status(400).json({ error: error.message });
      return;
    }

    const session = store.create(entryFile);
    const baseUrl = `${req.protocol}://${req.get("host")}`;

    res.status(201).json({
      sessionId: session.sessionId,
      url: `${baseUrl}/s/${session.sessionId}/`,
      entryFile: session.entryFile,
      rootDir: session.rootDir,
    });
  });

  app.get(/^\/s\/([^/]+)$/, (req, res) => {
    res.redirect(302, `/s/${req.params[0]}/`);
  });

  const previewRouter = express.Router({ mergeParams: true });

  previewRouter.get("/", async (req, res) => {
    const session = store.get(req.params.sessionId);

    if (!session) {
      res.status(404).type("text").send("Session not found.");
      return;
    }

    try {
      const html = await loadHtml(session.entryFile);
      res.type("html").send(html);
    } catch (error) {
      res.status(500).type("text").send(error.message);
    }
  });

  previewRouter.get(/.*/, async (req, res) => {
    const session = store.get(req.params.sessionId);

    if (!session) {
      res.status(404).type("text").send("Session not found.");
      return;
    }

    const resourcePath = req.path.replace(/^\/+/, "");

    if (!resourcePath) {
      res.status(404).type("text").send("Resource not found.");
      return;
    }

    const targetPath = path.resolve(session.rootDir, resourcePath);

    if (!isSubpath(session.rootDir, targetPath)) {
      res.status(403).type("text").send("Path escapes the session root.");
      return;
    }

    try {
      await ensureFile(targetPath);
      res.sendFile(targetPath);
    } catch (error) {
      if (error && error.code === "ENOENT") {
        res.status(404).type("text").send("Resource not found.");
        return;
      }

      res.status(400).type("text").send(error.message);
    }
  });

  app.use("/s/:sessionId", previewRouter);

  return { app, store };
}

function createServer(options = {}) {
  const host = options.host ?? "127.0.0.1";
  const port = options.port ?? 3939;
  const { app, store } = createApp(options);
  let server;
  let address;

  return {
    app,
    store,
    host,
    port,
    async start() {
      if (server) {
        return server;
      }

      server = http.createServer(app);

      await new Promise((resolve, reject) => {
        server.once("error", reject);
        server.listen(port, host, resolve);
      });

      address = server.address();
      return server;
    },
    async stop() {
      if (!server) {
        return;
      }

      await new Promise((resolve, reject) => {
        server.close((error) => {
          if (error) {
            reject(error);
            return;
          }

          resolve();
        });
      });

      server = undefined;
      address = undefined;
    },
    getAddress() {
      return address;
    },
    getOrigin() {
      if (!address || typeof address === "string") {
        return null;
      }

      return `http://${address.address}:${address.port}`;
    },
  };
}

module.exports = {
  createApp,
  createServer,
  createSessionStore,
  isHtmlFile,
  isSubpath,
};
