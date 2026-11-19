// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { MemoryScope } from "../../api/api";

const stroke = { fill: "none", stroke: "currentColor", strokeWidth: 1.5 } as const;

function Icon({ scope }: { scope: MemoryScope }) {
  return (
    <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
      {scope === "private" && (
        // A padlock: only the writer.
        <>
          <rect x="3.5" y="7" width="9" height="6.5" rx="1.2" {...stroke} />
          <path d="M5.5 7V5.2a2.5 2.5 0 015 0V7" {...stroke} />
        </>
      )}
      {scope === "team" && (
        // Two people: the writer's team.
        <>
          <circle cx="5.5" cy="5.5" r="2" {...stroke} />
          <circle cx="11" cy="6.2" r="1.6" {...stroke} />
          <path
            d="M1.8 13c.2-2.2 1.6-3.4 3.7-3.4s3.5 1.2 3.7 3.4M10 9.8c2 0 3.1 1 3.3 2.8"
            {...stroke}
          />
        </>
      )}
      {scope === "common" && (
        // A globe: everyone.
        <>
          <circle cx="8" cy="8" r="5.8" {...stroke} />
          <ellipse cx="8" cy="8" rx="2.4" ry="5.8" {...stroke} />
          <path d="M2.2 8h11.6" {...stroke} />
        </>
      )}
    </svg>
  );
}

/** A small badge that says who can read a note: an icon and the word, never the icon alone. */
export function ScopeBadge({ scope }: { scope: MemoryScope }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.memory.scope" });
  return (
    <span className={`scope-badge scope-badge--${scope}`}>
      <Icon scope={scope} />
      {t(scope)}
    </span>
  );
}
