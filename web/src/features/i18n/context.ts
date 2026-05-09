import { createContext } from "react";
import { dict, type TKey } from "./dict";

export type Locale = "en" | "zh";

type I18nContextValue = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: TKey) => string;
};

export const I18nContext = createContext<I18nContextValue>({
  locale: "en",
  setLocale: () => {},
  t: (key) => dict[key]?.en ?? key,
});