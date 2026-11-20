// SPDX-License-Identifier: MIT
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { PagedList } from "./paged-list";

/** The installed Debian packages, with a name search. */
export function PackagesSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.packages" });
  const api = useApi();
  const [draft, setDraft] = useState("");
  const [q, setQ] = useState("");

  return (
    <div className="stack-sm">
      <p className="hint">{t("lead")}</p>
      <form
        role="search"
        className="machine__search"
        onSubmit={(e) => {
          e.preventDefault();
          setQ(draft.trim());
        }}
      >
        <label className="field">
          {t("searchLabel")}
          <input
            type="search"
            value={draft}
            maxLength={100}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => {
              setDraft(e.target.value);
            }}
          />
        </label>
        <div className="row">
          <button type="submit" className="button button--primary" disabled={draft.trim() === ""}>
            {t("searchSubmit")}
          </button>
          {(q !== "" || draft !== "") && (
            <button
              type="button"
              className="button"
              onClick={() => {
                setDraft("");
                setQ("");
              }}
            >
              {t("searchClear")}
            </button>
          )}
        </div>
      </form>
      {q !== "" && <p className="hint">{t("resultsFor", { q })}</p>}
      <PagedList
        queryKey={["machine", "packages", q]}
        fetchPage={(cursor) =>
          api.listPackages({ ...(q !== "" && { q }), ...(cursor !== undefined && { cursor }) })
        }
        label={t("listLabel")}
        loading={t("loading")}
        empty={{ title: t("empty.title"), body: t("empty.body") }}
        itemKey={(p) => `${p.name}:${p.architecture}`}
        renderItem={(p) => (
          <>
            <p className="machine__title code">{p.name}</p>
            <p>{p.summary}</p>
            <p className="machine__meta">
              <span>{t("version", { version: p.version, architecture: p.architecture })}</span>
            </p>
          </>
        )}
      />
    </div>
  );
}
