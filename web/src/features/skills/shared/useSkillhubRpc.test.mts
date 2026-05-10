import test from "node:test";
import assert from "node:assert/strict";
import type { SkillhubConfig } from "@/features/rpc/types";

const { normalizeSkillhubConfig } = await import("./useSkillhubRpc.ts");

const baseConfig: SkillhubConfig = {
  registry: "https://skillhub.test",
  handle: "",
  loggedIn: false,
  offline: false,
  autoSync: true,
  learnedAutoPublish: false,
  subscriptions: [],
};

test("normalizeSkillhubConfig converts null subscriptions to an empty array", () => {
  const config = normalizeSkillhubConfig({
    ...baseConfig,
    subscriptions: null,
  } as unknown as SkillhubConfig);

  assert.deepEqual(config.subscriptions, []);
});

test("normalizeSkillhubConfig converts missing subscriptions to an empty array", () => {
  const config = normalizeSkillhubConfig({
    ...baseConfig,
    subscriptions: undefined,
  } as unknown as SkillhubConfig);

  assert.deepEqual(config.subscriptions, []);
});

test("normalizeSkillhubConfig keeps only string subscription slugs", () => {
  const config = normalizeSkillhubConfig({
    ...baseConfig,
    subscriptions: ["alpha", null, 42, "beta"],
  } as unknown as SkillhubConfig);

  assert.deepEqual(config.subscriptions, ["alpha", "beta"]);
});
