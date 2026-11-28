import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { TaskResponse } from "./types";

const tasks = [
  {
    id: "t-1",
    title: "Migrate the board",
    status: "in_progress",
    phase: "execution",
    vault: "private-personal",
    obsidian_url: "obsidian://open?vault=private-personal&file=25%20Tasks%2FMigrate.md",
    blocked_by: [],
    blocked: false,
    blockers: [],
    upcoming: false,
    recently_completed: false,
    goals: [],
    flag: false,
    priority: 1,
    open_questions: [],
  },
] as unknown as TaskResponse[];

// routeMock answers per endpoint so a test can fail one route without failing
// the others — the shape the banner assertions need.
function routeMock(routes: Record<string, unknown>) {
  return vi.fn().mockImplementation((url: string) => {
    for (const [prefix, body] of Object.entries(routes)) {
      if (String(url).startsWith(prefix)) {
        return Promise.resolve({ ok: true, status: 200, json: async () => body });
      }
    }
    return Promise.resolve({ ok: false, status: 404, json: async () => ({}) });
  });
}

describe("App", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    window.localStorage.clear();
  });
  afterEach(() => vi.unstubAllGlobals());

  describe("with a healthy backend", () => {
    beforeEach(() => {
      vi.stubGlobal(
        "fetch",
        routeMock({
          "/api/tasks": tasks,
          "/api/source-status": { unavailable: false },
          "/api/vaults": [{ name: "private-personal", source: true }],
          "/api/assignees": { named: ["e2e"], has_unassigned: false },
        }),
      );
    });

    it("renders the tasks the API returned, not hardcoded ones", async () => {
      render(<App />);
      expect(await screen.findByRole("link", { name: /Migrate the board/ })).toBeInTheDocument();
    });

    it("links a card's title to the in-app page view, not to Obsidian", async () => {
      render(<App />);
      const link = await screen.findByRole("link", { name: /Migrate the board/ });
      expect(link).toHaveAttribute("href", "/v/private-personal/page/25%20Tasks/Migrate.md");
      expect(link.getAttribute("href")).not.toMatch(/^obsidian:/);
    });

    it("keeps both banners hidden", async () => {
      render(<App />);
      await screen.findByRole("link", { name: /Migrate the board/ });
      expect(screen.getByTestId("vault-api-unavailable")).toHaveClass("hidden");
      expect(screen.getByTestId("vault-no-source")).toHaveClass("hidden");
    });
  });

  describe("with a failing task endpoint", () => {
    beforeEach(() => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue({ ok: false, status: 500, json: async () => ({}) }),
      );
    });

    it("shows an error instead of a blank page when the API fails", async () => {
      render(<App />);
      expect(await screen.findByText(/Could not read the board/)).toBeInTheDocument();
    });
  });

  describe("with vault-api down", () => {
    beforeEach(() => {
      vi.stubGlobal(
        "fetch",
        routeMock({
          "/api/tasks": tasks,
          "/api/source-status": { unavailable: true },
          "/api/vaults": [{ name: "private-personal", source: true }],
          "/api/assignees": { named: [], has_unassigned: false },
        }),
      );
    });

    it("raises the outage banner and still renders the last-known board", async () => {
      render(<App />);
      await screen.findByRole("link", { name: /Migrate the board/ });
      expect(screen.getByTestId("vault-api-unavailable")).not.toHaveClass("hidden");
    });
  });
});
