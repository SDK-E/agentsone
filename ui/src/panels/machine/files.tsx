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
      {folder.isError && <LoadError error={folder.error} onRetry={() => void folder.refetch()} />}
      {folder.isSuccess && (
        <>
          <p className="machine__here code">{t("here", { path: here })}</p>
          {entries.length === 0 ? (
            <div className="empty">
              <h4>{t("empty.title")}</h4>
              <p>{t("empty.body")}</p>
            </div>
          ) : (
            <ul className="machine__list" aria-label={t("listLabel")}>
              {entries.map((entry) => (
                <li key={entry.path} className="machine__item">
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
  const meta = t("meta", {
    size: formatBytes(entry.size_bytes),
    owner: entry.owner,
    mode: entry.mode,
  });
  return (
    <>
      <div className="machine__head">
        {entry.type === "directory" ? (
          <button
            type="button"
            className="machine__link"
            aria-label={t("openFolder", { name: entry.name })}
            onClick={onFolder}
          >
            {entry.name}/
          </button>
        ) : entry.type === "file" ? (
          <button
            type="button"
            className="machine__link"
            aria-label={t("readFile", { name: entry.name })}
            onClick={onFile}
          >
            {entry.name}
          </button>
        ) : (
          <p className="machine__title">{entry.name}</p>
        )}
        <span className="chip">{t(`type.${entry.type}`)}</span>
      </div>
      <p className="machine__meta">
        <span>{meta}</span>
      </p>
    </>
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
