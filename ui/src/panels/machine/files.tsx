// SPDX-License-Identifier: MIT
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import type { FileEntry } from "../../api/api";
import { useApi } from "../../app/api-context";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";
import { formatBytes } from "../monitor/format";

const START = "/";

/** The folder above `path`: "/home/ada" gives "/home", "/" stays "/". */
export function parentOf(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  const cut = trimmed.lastIndexOf("/");
  return cut <= 0 ? "/" : trimmed.slice(0, cut);
}

/** Look through folders and read text files, as the signed-in person's Linux user. */
export function FilesSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.files" });
  const tm = useTranslation(undefined, { keyPrefix: "panels.machine" }).t;
  const api = useApi();
  const [path, setPath] = useState(START);
  /** What the person typed in the folder field; until they type, it shows where they are. */
  const [draft, setDraft] = useState<string | undefined>();
  const [open, setOpen] = useState<FileEntry | undefined>();

  const folder = useInfiniteQuery({
    queryKey: ["machine", "files", path],
    queryFn: ({ pageParam }) => api.listFiles(path, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
  });
  // The daemon names the folder it listed; that is the one the person is in.
  const here = folder.data?.pages[0]?.path ?? path;
  const entries = folder.data?.pages.flatMap((page) => page.items) ?? [];

  const go = (next: string) => {
    setPath(next);
    setDraft(undefined);
    setOpen(undefined);
  };

  return (
    <div className="stack-sm">
      <p className="hint">{t("lead")}</p>
      <form
        className="machine__search"
        onSubmit={(e) => {
          e.preventDefault();
          const next = (draft ?? here).trim();
          if (next.startsWith("/")) go(next);
        }}
      >
        <label className="field">
          {t("pathLabel")}
          <input
            type="text"
            value={draft ?? here}
            maxLength={4096}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            pattern="/.*"
            onChange={(e) => {
              setDraft(e.target.value);
            }}
          />
        </label>
        <div className="row">
          <button type="submit" className="button button--primary">
            {t("open")}
          </button>
          <button
            type="button"
            className="button"
            disabled={here === "/"}
            onClick={() => {
              go(parentOf(here));
            }}
          >
            {t("up")}
          </button>
        </div>
      </form>

      {folder.isPending && <Loading label={t("loading")} />}
      {folder.isError && (
        <LoadError
          error={folder.error}
          onRetry={() => void folder.refetch()}
          forbidden={t("forbidden")}
          invalid={t("invalid")}
          action={
            here === "/" ? undefined : (
              <button
                type="button"
                className="button"
                onClick={() => {
                  go(parentOf(here));
                }}
              >
                {t("up")}
              </button>
            )
          }
        />
      )}
      {folder.isSuccess && (
        <>
          <p className="machine__here code">{t("here", { path: here })}</p>
          {entries.length === 0 ? (
            <div className="empty">
              <h4>{t("empty.title")}</h4>
              <p>{t("empty.body")}</p>
            </div>
          ) : (
            <ul className="machine__rows" aria-label={t("listLabel")}>
              {entries.map((entry) => (
                <li key={entry.path} className="machine__row">
                  <Entry
                    entry={entry}
                    onFolder={() => {
                      go(entry.path);
                    }}
                    onFile={() => {
                      setOpen(entry);
                    }}
                  />
                </li>
              ))}
            </ul>
          )}
          {folder.hasNextPage && (
            <button
              type="button"
              className="button"
              disabled={folder.isFetchingNextPage}
              onClick={() => void folder.fetchNextPage()}
            >
              {folder.isFetchingNextPage ? tm("loadingMore") : tm("showMore")}
            </button>
          )}
        </>
      )}

      {open && (
        <FileView
          entry={open}
          onClose={() => {
            setOpen(undefined);
          }}
        />
      )}
    </div>
  );
}

function Entry({
  entry,
  onFolder,
  onFile,
}: {
  entry: FileEntry;
  onFolder: () => void;
  onFile: () => void;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.files" });
  // The kind is said in words too: "README.md, File". The icon alone is not enough.
  const said = `${entry.name}, ${t(`type.${entry.type}`)}`;
  const name = (
    <>
      <TypeIcon type={entry.type} />
      <span className="machine__name">{entry.name}</span>
    </>
  );
  const onOpen = entry.type === "directory" ? onFolder : entry.type === "file" ? onFile : undefined;
  return (
    <>
      {onOpen ? (
        <button type="button" className="machine__open" aria-label={said} onClick={onOpen}>
          {name}
        </button>
      ) : (
        <span className="machine__open machine__open--still">
          {name}
          <span className="visually-hidden">{`, ${t(`type.${entry.type}`)}`}</span>
        </span>
      )}
      <span className="machine__size">{formatBytes(entry.size_bytes)}</span>
      <span className="machine__owner">{entry.owner}</span>
    </>
  );
}

/** A small picture of the entry's kind. The kind is also said in words for screen readers. */
function TypeIcon({ type }: { type: FileEntry["type"] }) {
  const paths: Record<FileEntry["type"], string> = {
    directory: "M2 4.5h4l1.5 1.5H14v6.5H2z",
    file: "M4 1.5h5l3 3v10H4z M9 1.5v3h3",
    symlink: "M6 10l4-4 M7 6h3v3 M3 3.5h10v9H3z",
    other: "M8 2.5a5.5 5.5 0 1 0 0.01 0z",
  };
  return (
    <svg className="machine__icon" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      <path d={paths[type]} />
    </svg>
  );
}

function FileView({ entry, onClose }: { entry: FileEntry; onClose: () => void }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.files" });
  const api = useApi();
  const file = useQuery({
    queryKey: ["machine", "file", entry.path],
    queryFn: () => api.readFile(entry.path),
    retry: false,
  });
  const headingId = `machine-file-${entry.path.replace(/[^a-zA-Z0-9]/g, "-")}`;
  // The file opens below the list; take the person (and their screen reader) to it.
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, [entry.path]);

  return (
    <section className="card stack-sm" aria-labelledby={headingId}>
      <div className="machine__head">
        <h4 id={headingId} ref={heading} tabIndex={-1} className="code">
          {entry.path}
        </h4>
        <button type="button" className="button" onClick={onClose}>
          {t("closeFile")}
        </button>
      </div>
      <p className="machine__meta">
        <span>
          {t("meta", {
            size: formatBytes(entry.size_bytes),
            owner: `${entry.owner}:${entry.group}`,
            mode: entry.mode,
          })}
        </span>
      </p>
      {file.isPending && <Loading label={t("reading")} />}
      {file.isError && (
        <div role="alert" className="notice notice--danger stack-sm">
          <p>{t("readFailed")}</p>
          {problemText(file.error, "") !== "" && (
            <p className="notice__detail">{problemText(file.error, "")}</p>
          )}
        </div>
      )}
      {file.data?.encoding === "base64" && (
        <p>{t("binary", { size: formatBytes(file.data.size_bytes) })}</p>
      )}
      {file.data?.encoding === "utf-8" && (
        <>
          {file.data.truncated && <p className="hint">{t("truncated")}</p>}
          <pre className="machine__content">{file.data.content}</pre>
        </>
      )}
    </section>
  );
}
