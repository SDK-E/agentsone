// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import { TalkBox } from "../router/talk-box";
import { LiveSociety } from "../society/live-society";
import { PanelRequestProvider } from "./panel-request";
import { PanelsArea } from "./panel-tabs";

/** Home: talk box on the left, the live society on the right, panels below. */
export function Home() {
  const { t } = useTranslation();
  return (
    <PanelRequestProvider>
      <h1 className="visually-hidden">{t("home.title")}</h1>
      <div className="home">
        <TalkBox />
        <LiveSociety />
      </div>
      <PanelsArea />
    </PanelRequestProvider>
  );
}
