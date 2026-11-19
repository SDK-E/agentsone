// SPDX-License-Identifier: MIT
import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const repoRoot = fileURLToPath(new URL("../../../", import.meta.url));

export function repoPath(relative: string): string {
  return `${repoRoot}${relative}`;
}

export function loadJson(path: string): unknown {
  return JSON.parse(readFileSync(path, "utf8"));
}

/** Compile one schema from contract/schemas, with every sibling schema loaded so $refs resolve. */
export function validatorFor(schemaFile: string): ValidateFunction {
  const dir = repoPath("contract/schemas");
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  for (const file of readdirSync(dir).filter((f) => f.endsWith(".schema.json"))) {
    if (file !== schemaFile) ajv.addSchema(loadJson(`${dir}/${file}`) as object);
  }
  return ajv.compile(loadJson(`${dir}/${schemaFile}`) as object);
}

interface TokenNode {
  $value?: string;
  [key: string]: unknown;
}

/** The light or dark role colours from brand/colours/tokens.json, with references resolved. */
export function brandRoles(mode: "light" | "dark"): Record<string, string> {
  const tokens = loadJson(repoPath("brand/colours/tokens.json")) as Record<string, TokenNode>;
  const resolve = (value: string): string => {
    const ref = /^\{(.+)\}$/.exec(value)?.[1];
    if (ref === undefined) return value;
    const node = ref.split(".").reduce<unknown>((at, key) => (at as TokenNode)[key], tokens);
    return resolve((node as TokenNode).$value ?? "");
  };
  const roles: Record<string, string> = {};
  for (const [role, node] of Object.entries(tokens[mode] ?? {})) {
    if (role.startsWith("$")) continue;
    roles[role] = resolve((node as TokenNode).$value ?? "");
  }
  return roles;
}
