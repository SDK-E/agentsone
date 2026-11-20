// SPDX-License-Identifier: MIT
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { blocksOf, ReasonText, spansOf } from "./reason-text";

describe("Reason text", () => {
  it("shows plain text as it is", () => {
    const { container } = render(<ReasonText text="The host has no cgroup v2." />);
    expect(container.querySelectorAll("p")).toHaveLength(1);
    expect(screen.getByText("The host has no cgroup v2.")).toBeVisible();
    expect(container.querySelector("ul, code")).toBeNull();
  });

  it("makes lines that start with a dash and a space into one list", () => {
    render(<ReasonText text={"Why.\nTo fix:\n- One.\n- Two.\nAfter."} />);
    const items = screen.getAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual(["One.", "Two."]);
    expect(screen.getAllByRole("list")).toHaveLength(1);
    expect(screen.getByText("After.").tagName).toBe("P");
  });

  it("shows text between backticks as code", () => {
    render(
      <ReasonText text={"- Keep `cgroup: private` and `security_opt: writable-cgroups=true`."} />,
    );
    expect(screen.getByText("cgroup: private").tagName).toBe("CODE");
    expect(screen.getByText("security_opt: writable-cgroups=true").tagName).toBe("CODE");
    expect(screen.getByRole("listitem")).toHaveTextContent(
      "Keep cgroup: private and security_opt: writable-cgroups=true.",
    );
  });

  it("never reads HTML or other Markdown", () => {
    const { container } = render(
      <ReasonText
        text={"<b>bold</b> **star** [link](https://example.org)\n-not a list\n# not a heading"}
      />,
    );
    expect(container.querySelector("b, strong, a, h1, ul")).toBeNull();
    expect(screen.getByText("<b>bold</b> **star** [link](https://example.org)")).toBeVisible();
    expect(screen.getByText("-not a list")).toBeVisible();
  });

  it("keeps a backtick without its pair, and drops blank lines", () => {
    expect(spansOf("a `b` c `d")).toEqual([
      { code: false, text: "a " },
      { code: true, text: "b" },
      { code: false, text: " c `d" },
    ]);
    expect(spansOf("`")).toEqual([{ code: false, text: "`" }]);
    expect(spansOf("``")).toEqual([]);
    expect(blocksOf("\n  \nWhy.\n\n- \n")).toEqual([
      { kind: "line", spans: [{ code: false, text: "Why." }] },
      { kind: "line", spans: [{ code: false, text: "-" }] },
    ]);
  });
});
