import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { TaskResponse } from "./types";

const tasks = [
  {
    id: "t-1",
    title: "Migrate the board",
    status: "in_progress",
    vault: "private-personal",
    obsidian_url: "obsidian://open?vault=private-personal&file=x",
  },
] as unknown as TaskResponse[];

describe("App", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => tasks }));
  });
  afterEach(() => vi.unstubAllGlobals());

  it("renders the tasks the API returned, not hardcoded ones", async () => {
    render(<App />);
    expect(await screen.findByText("Migrate the board")).toBeInTheDocument();
  });

  it("shows an error instead of a blank page when the API fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 500 }));
    render(<App />);
    expect(await screen.findByText(/Could not read the board/)).toBeInTheDocument();
  });
});
