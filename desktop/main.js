// @ts-check
// The desktop app: the gateway's page in a window of its own. The gateway
// does the work; the app makes sure one runs, opens its page with the
// token, and sends links that leave the page to the browser. Closing the
// window leaves the gateway, and the agents it runs, going.

import { app, BrowserWindow, dialog, session, shell } from "electron";
import { execFile } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

// `gateway start` waits for the projects to connect, up to 20s.
const START_TIMEOUT_MS = 40_000;
const OPEN_TIMEOUT_MS = 10_000;

// Permissions the page may have: notifications, and copying from the
// terminal. Everything else (camera, geolocation, …) is denied.
const ALLOWED_PERMISSIONS = new Set(["notifications", "clipboard-sanitized-write"]);

/** Login shells that run the POSIX script in run(). */
const POSIX_SHELLS = new Set(["zsh", "bash", "sh", "dash", "ksh"]);

/**
 * The claude-bot binary: $CLAUDE_BOT_BIN, the one inside the app, the
 * repository's own build when run from it, else the one on the login
 * shell's PATH.
 */
function claudeBot() {
  if (process.env.CLAUDE_BOT_BIN) return process.env.CLAUDE_BOT_BIN;
  if (app.isPackaged) return path.join(process.resourcesPath, "claude-bot");

  const dev = path.resolve(here, "../bin/claude-bot");
  if (!app.isPackaged && fs.existsSync(dev)) return dev;

  return "claude-bot";
}

/**
 * Runs claude-bot in a login shell, so the gateway it starts, and the
 * agents it runs, get the PATH and environment of the user's terminal,
 * not the bare one apps get. The binary is passed as $0, never spliced
 * into the command.
 *
 * @param {string[]} args
 * @param {number} timeout
 * @returns {Promise<string>} what it printed
 */
function run(args, timeout) {
  // The script below is POSIX sh; a fish or nushell login shell would reject it.
  const user = process.env.SHELL ?? "";
  const sh = POSIX_SHELLS.has(path.basename(user)) ? user : "/bin/zsh";
  const script = `exec "$0" ${args.map((a) => `'${a}'`).join(" ")}`;

  return new Promise((resolve, reject) => {
    execFile(sh, ["-l", "-c", script, claudeBot()], { timeout }, (err, stdout, stderr) => {
      if (err) reject(new Error((stderr || stdout || err.message).trim()));
      else resolve(stdout);
    });
  });
}

/**
 * The app is not notarized: a downloaded copy is quarantined, and so is the
 * binary inside it, which the gateway then runs outside the app. Once the
 * app is open, the person has let it run: the binary may run too.
 */
function trustBundledBinary() {
  if (!app.isPackaged || process.platform !== "darwin") return Promise.resolve();

  return new Promise((resolve) => {
    execFile("/usr/bin/xattr", ["-d", "com.apple.quarantine", claudeBot()], () => resolve(undefined));
  });
}

/** Starts the gateway if it is not running, and returns its page with the token. */
async function gatewayPage() {
  await trustBundledBinary();
  await run(["gateway", "start"], START_TIMEOUT_MS);

  const out = await run(["gateway", "open", "--print"], OPEN_TIMEOUT_MS);
  const link = out
    .split("\n")
    .map((l) => l.trim())
    .find((l) => /^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):\d+\//.test(l));
  if (!link) throw new Error(`claude-bot gateway open printed no address:\n${out}`);

  return new URL(link);
}

/** @param {string} url */
function openExternal(url) {
  try {
    const { protocol } = new URL(url);
    if (protocol === "https:" || protocol === "http:" || protocol === "mailto:") void shell.openExternal(url);
  } catch {
    // Not a URL: ignore.
  }
}

/** @param {URL} page */
function createWindow(page) {
  const win = new BrowserWindow({
    width: 1360,
    height: 900,
    minWidth: 760,
    minHeight: 560,
    title: "claude-bot",
    show: false,
    webPreferences: {
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      spellcheck: false,
    },
  });

  // The page stays the page: anything else opens in the browser.
  const ours = (/** @type {string} */ url) => {
    try {
      return new URL(url).origin === page.origin;
    } catch {
      return false;
    }
  };

  win.webContents.setWindowOpenHandler(({ url }) => {
    openExternal(url);

    return { action: "deny" };
  });
  win.webContents.on("will-navigate", (e, url) => {
    if (!ours(url)) {
      e.preventDefault();
      openExternal(url);
    }
  });

  win.once("ready-to-show", () => win.show());
  void win.loadURL(page.href);

  return win;
}

async function open() {
  try {
    createWindow(await gatewayPage());
  } catch (err) {
    const choice = await dialog.showMessageBox({
      type: "error",
      title: "claude-bot",
      message: "gateway 没能启动",
      detail: `${err instanceof Error ? err.message : String(err)}\n\n在终端里运行 claude-bot gateway start 看看原因。`,
      buttons: ["重试", "退出"],
      defaultId: 0,
      cancelId: 1,
    });
    if (choice.response === 0) return open();
    app.quit();
  }
}

/** @type {Promise<void> | null} */
let opening = null;

/**
 * open, unless it is already under way: clicking the Dock icon while the
 * gateway starts (no window yet) must not start it, or ask, a second time.
 */
function openOnce() {
  opening ??= open().finally(() => {
    opening = null;
  });

  return opening;
}

if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on("second-instance", () => {
    const [win] = BrowserWindow.getAllWindows();
    if (win) {
      if (win.isMinimized()) win.restore();
      win.focus();
    }
  });

  app.whenReady().then(() => {
    session.defaultSession.setPermissionRequestHandler((_wc, permission, callback) => callback(ALLOWED_PERMISSIONS.has(permission)));
    session.defaultSession.setPermissionCheckHandler((_wc, permission) => ALLOWED_PERMISSIONS.has(permission));

    void openOnce();

    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) void openOnce();
    });
  });

  // The gateway keeps running without the window.
  app.on("window-all-closed", () => {
    if (process.platform !== "darwin") app.quit();
  });
}
