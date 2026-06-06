import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const distDir = path.join(rootDir, "dist");

const envPath = path.join(rootDir, ".env");
const lucideIconDir = path.join(rootDir, "node_modules", "lucide-static", "icons");
const lucideIconNames = [
  "badge-check",
  "check",
  "clipboard-copy",
  "circle-question-mark",
  "crown",
  "link-2",
  "mouse-pointer-click",
  "refresh-ccw-dot",
  "refresh-cw",
  "radio-receiver",
  "settings",
  "sliders-horizontal",
  "trash-2",
  "users",
  "wifi-sync",
  "x",
];
const firebaseConfigFields = {
  apiKey: "FIREBASE_API_KEY",
  authDomain: "FIREBASE_AUTH_DOMAIN",
  projectId: "FIREBASE_PROJECT_ID",
  storageBucket: "FIREBASE_STORAGE_BUCKET",
  messagingSenderId: "FIREBASE_MESSAGING_SENDER_ID",
  appId: "FIREBASE_APP_ID",
  measurementId: "FIREBASE_MEASUREMENT_ID",
  databaseURL: "FIREBASE_DATABASE_URL",
};

function parseEnv(source) {
  const env = {};

  for (const rawLine of source.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;

    const separatorIndex = line.indexOf("=");
    if (separatorIndex === -1) continue;

    const key = line.slice(0, separatorIndex).trim();
    let value = line.slice(separatorIndex + 1).trim();

    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }

    env[key] = value;
  }

  return env;
}

async function loadEnv() {
  try {
    return parseEnv(await readFile(envPath, "utf8"));
  } catch (error) {
    if (error.code === "ENOENT") return {};
    throw error;
  }
}

async function loadLucideIcons() {
  const icons = {};

  for (const iconName of lucideIconNames) {
    const svg = await readFile(path.join(lucideIconDir, `${iconName}.svg`), "utf8");
    icons[iconName] = svg
      .replace(/<svg[^>]*>/, "")
      .replace("</svg>", "")
      .trim();
  }

  return icons;
}

function buildFirebaseConfig(env) {
  return Object.fromEntries(
    Object.entries(firebaseConfigFields).map(([configKey, envKey]) => [
      configKey,
      env[envKey] || "",
    ]),
  );
}

async function buildUserscript(inputName, outputName, firebaseConfig, lucideIcons) {
  const inputPath = path.join(rootDir, "src", inputName);
  const outputPath = path.join(distDir, outputName);
  const source = await readFile(inputPath, "utf8");
  const output = source
    .replace("__DEFAULT_FIREBASE_CONFIG__", JSON.stringify(firebaseConfig, null, 8))
    .replace("__LUCIDE_ICONS__", JSON.stringify(lucideIcons, null, 8));

  if (output.includes("__DEFAULT_FIREBASE_CONFIG__") || output.includes("__LUCIDE_ICONS__")) {
    throw new Error(`Build token not found in ${inputName}`);
  }

  await writeFile(outputPath, output, "utf8");
  console.log(`Built ${outputName}`);
}

const env = await loadEnv();
const firebaseConfig = buildFirebaseConfig(env);
const lucideIcons = await loadLucideIcons();

await mkdir(distDir, { recursive: true });
await buildUserscript("host.user.js", "host.user.js", firebaseConfig, lucideIcons);
await buildUserscript("guest.user.js", "guest.user.js", firebaseConfig, lucideIcons);
