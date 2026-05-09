import { dictCore, type DictCoreKey } from "./dict-core";
import { dictSettings, type DictSettingsKey } from "./dict-settings";
import { dictSkills, type DictSkillsKey } from "./dict-skills";
import { dictCanvas, type DictCanvasKey } from "./dict-canvas";
import { dictApps, type DictAppsKey } from "./dict-apps";
import { dictTasks, type DictTasksKey } from "./dict-tasks";
import { dictAuthProject, type DictAuthProjectKey } from "./dict-auth-project";
import { dictUiExtras, type DictUiExtrasKey } from "./dict-ui-extras";
import { dictSkillhubStorage, type DictSkillhubStorageKey } from "./dict-skillhub-storage";

export const dict = {
  ...dictCore,
  ...dictSettings,
  ...dictSkills,
  ...dictCanvas,
  ...dictApps,
  ...dictTasks,
  ...dictAuthProject,
  ...dictUiExtras,
  ...dictSkillhubStorage,
} as const;

export type TKey =
  | DictCoreKey
  | DictSettingsKey
  | DictSkillsKey
  | DictCanvasKey
  | DictAppsKey
  | DictTasksKey
  | DictAuthProjectKey
  | DictUiExtrasKey
  | DictSkillhubStorageKey;