// SPDX-License-Identifier: MIT
//
// Every visible string lives in a locale file. English is the only locale today; a new
// language is a new file and one more entry in `resources`.
import i18next from "i18next";
import { initReactI18next } from "react-i18next";

import en from "./en.json";

export const i18n = i18next.createInstance();

void i18n.use(initReactI18next).init({
  lng: "en",
  fallbackLng: "en",
  resources: { en: { translation: en } },
  interpolation: { escapeValue: false },
  initAsync: false,
});
