// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { PagedList } from "./paged-list";

/** The Linux accounts: people, agents and the system's own. */
export function UsersSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.users" });
  const api = useApi();
  return (
    <div className="stack-sm">
      <p className="hint">{t("lead")}</p>
      <PagedList
        queryKey={["machine", "users"]}
        fetchPage={(cursor) => api.listMachineUsers(cursor)}
        label={t("listLabel")}
        loading={t("loading")}
        empty={{ title: t("empty.title"), body: t("empty.body") }}
        itemKey={(u) => u.name}
        renderItem={(u) => (
          <>
            <div className="machine__head">
              <p className="machine__title">{u.name}</p>
              <span className="chip">{t(`kind.${u.kind}`)}</span>
            </div>
            <p className="machine__meta">
              <span>{t("uid", { uid: u.uid })}</span>
              <span>{t("home", { home: u.home })}</span>
              <span>{t("shell", { shell: u.shell })}</span>
            </p>
          </>
        )}
      />
    </div>
  );
}
