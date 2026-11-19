// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";

import type { InstalledTheme } from "../../api/api";
import { useApi } from "../../app/api-context";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";
import { ModeSwitch } from "../../theme/mode-switch";
import { defaultTheme, type Theme } from "../../theme/theme";
import { useTheme, useThemeSelection } from "../../theme/theme-provider";

/** Light, dark or system, and which installed theme the interface wears. */
export function LookSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.look" });
  const api = useApi();
  const client = useQueryClient();
  const titleId = useId();
  const { mode, choose } = useThemeSelection();
  const { setTheme } = useTheme();

  const themes = useQuery({ queryKey: ["themes"], queryFn: () => api.listThemes(), retry: false });
  const selection = useQuery({
    queryKey: ["theme-selection"],
    queryFn: () => api.getThemeSelection(),
    retry: false,
  });
  const pick = useMutation({
    mutationFn: (theme: InstalledTheme) => api.setThemeSelection({ theme_id: theme.id }),
    onSuccess: (saved, theme) => {
      client.setQueryData(["theme-selection"], saved);
      // The API and the theme schema describe the same document; the schema's tuple type for
      // contrastPairs is stricter than the API's array type.
      setTheme(theme.builtin ? defaultTheme : (theme.theme as Theme));
    },
  });
  const current = pick.isPending ? pick.variables.id : selection.data?.theme_id;

  return (
    <section aria-labelledby={titleId} className="stack">
      <h4 id={titleId}>{t("title")}</h4>
      <ModeSwitch mode={mode} onChange={choose} />

      {themes.isPending && <Loading />}
      {themes.isError && <LoadError error={themes.error} onRetry={() => void themes.refetch()} />}
      {themes.isSuccess && (
        <fieldset className="choices">
          <legend>{t("themeLabel")}</legend>
          {themes.data.map((theme) => (
            <label key={theme.id} className="choice">
              <input
                type="radio"
                name="theme"
                value={theme.id}
                checked={current === theme.id}
                onChange={() => {
                  pick.mutate(theme);
                }}
              />
              <span>{theme.name}</span>
            </label>
          ))}
          {themes.data.length <= 1 && <p className="hint">{t("themeHint")}</p>}
        </fieldset>
      )}
      {pick.isError && (
        <p role="alert" className="notice notice--danger">
          {problemText(pick.error, t("saveFailed"))}
        </p>
      )}
    </section>
  );
}
