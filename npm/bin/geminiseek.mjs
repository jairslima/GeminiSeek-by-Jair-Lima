#!/usr/bin/env node
/**
 * GeminiSeek by Jair Lima — Cross-platform Node.js ESM Launcher
 * Copyright (c) 2026 Jair Lima. Licensed under MIT & Apache-2.0.
 */
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const APP_NAME = "GeminiSeek by Jair Lima";
const APP_VERSION = "1.1.0";

function findNativeBinary() {
  if (process.env.GEMINISEEK_BIN && fs.existsSync(process.env.GEMINISEEK_BIN)) {
    return process.env.GEMINISEEK_BIN;
  }
  const exeName = process.platform === "win32" ? "geminiseek.exe" : "geminiseek";
  const binCandidate = path.join(os.homedir(), "bin", exeName);
  if (fs.existsSync(binCandidate)) {
    return binCandidate;
  }
  return exeName;
}

const rawArgs = process.argv.slice(2);
const bin = findNativeBinary();

const child = spawn(bin, rawArgs, {
  stdio: "inherit",
  env: process.env,
});

child.on("error", (err) => {
  console.error(`[${APP_NAME} v${APP_VERSION}] Erro ao iniciar binário nativo (${bin}): ${err.message}`);
  process.exit(1);
});

child.on("exit", (code) => process.exit(code ?? 1));
