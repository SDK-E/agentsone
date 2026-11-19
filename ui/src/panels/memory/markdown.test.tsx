// SPDX-License-Identifier: MIT
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { Markdown } from "./markdown";

describe("Markdown", () => {
  it("shows headings, paragraphs and lists as real elements", () => {
    render(
      <Markdown
        text={"# Plan\n\nFirst paragraph.\n\n- one\n- two\n\n1. step a\n2. step b"}
        headingLevel={4}
      />,
    );

    expect(screen.getByRole("heading", { level: 4, name: "Plan" })).toBeVisible();
    expect(screen.getByText("First paragraph.")).toBeVisible();
    expect(screen.getAllByRole("list")).toHaveLength(2);
    expect(screen.getAllByRole("listitem")).toHaveLength(4);
  });

  it("shows bold, italic and inline code", () => {
    const { container } = render(<Markdown text={"A **strong** and *soft* word with `code`."} />);

    expect(container.querySelector("strong")).toHaveTextContent("strong");
    expect(container.querySelector("em")).toHaveTextContent("soft");
    expect(container.querySelector("code")).toHaveTextContent("code");
  });

  it("keeps a fenced code block as plain text", () => {
    const { container } = render(<Markdown text={"```\npnpm install\n<b>not bold</b>\n```"} />);

    expect(container.querySelector("pre")).toHaveTextContent("pnpm install");
    expect(container.querySelector("pre")).toHaveTextContent("<b>not bold</b>");
    expect(container.querySelector("b")).toBeNull();
  });

  it("opens web links safely and drops other kinds of link", () => {
    render(<Markdown text={"[Docs](https://example.com/docs) and [bad](javascript:alert(1))"} />);

    const link = screen.getByRole("link", { name: "Docs" });
    expect(link).toHaveAttribute("href", "https://example.com/docs");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(link).toHaveAttribute("target", "_blank");
    expect(screen.queryByRole("link", { name: "bad" })).toBeNull();
    expect(screen.getByText(/bad/)).toBeVisible();
  });

  it("never turns written HTML into elements", () => {
    const { container } = render(
      <Markdown text={'<img src=x onerror="alert(1)"> <script>1</script>'} />,
    );

    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("script")).toBeNull();
    expect(container).toHaveTextContent("<script>1</script>");
  });

  it("has no accessibility violations", async () => {
    const { container } = render(<Markdown text={"# T\n\n- a\n- b\n\n[x](https://example.com)"} />);

    await expectNoAxeViolations(container);
  });
});
