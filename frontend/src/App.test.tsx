import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  describe("when the server refuses a queued write", () => {
    // FakeEventSource stands in for the stream jsdom lacks; the test emits
    // events by name, exactly as the server's `event:` line names them.
    class FakeEventSource {
      static current: FakeEventSource | null = null;
      private listeners = new Map<string, ((event: MessageEvent) => void)[]>();
      constructor(public url: string) {
        FakeEventSource.current = this;
      }
      addEventListener(name: string, listener: (event: MessageEvent) => void) {
        this.listeners.set(name, [...(this.listeners.get(name) ?? []), listener]);
      }
      close() {}
      emit(name: string, data = "{}") {
        act(() => {
          (this.listeners.get(name) ?? []).forEach((listener) =>
            listener(new MessageEvent(name, { data })),
          );
        });
      }
    }

    beforeEach(() => {
      FakeEventSource.current = null;
      vi.stubGlobal("EventSource", FakeEventSource);
      const routes = routeMock({
        // The file never changes: every read returns the unflagged card.
        "/api/tasks": tasks,
        "/api/source-status": { unavailable: false },
        "/api/vaults": [{ name: "private-personal", source: true }],
        "/api/assignees": { named: [], has_unassigned: false },
      });
      vi.stubGlobal(
        "fetch",
        vi.fn().mockImplementation((url: string, init?: RequestInit) => {
          if (init?.method === "PATCH") {
            return Promise.resolve({
              ok: true,
              status: 202,
              json: async () => ({ status: "success", task_id: "t-1", flag: true }),
            });
          }
          return routes(url);
        }),
      );
    });

    it("drops the pending overlay, re-reads and shows the reason", async () => {
      render(<App />);
      fireEvent.click(await screen.findByTitle(/^Flag/));
      expect(await screen.findByTitle(/^Unflag/)).toBeInTheDocument();

      // The negative control: a plain wake-up re-reads the unchanged file, and
      // the pending overlay keeps the optimistic value on the card.
      const taskReads = () =>
        vi
          .mocked(fetch)
          .mock.calls.filter(
            ([url, init]) => String(url).startsWith("/api/tasks?") && init?.method !== "PATCH",
          ).length;
      const readsBefore = taskReads();
      FakeEventSource.current!.emit("board");
      await waitFor(() => expect(taskReads()).toBeGreaterThan(readsBefore));
      await act(async () => undefined);
      expect(screen.getByTitle(/^Unflag/)).toBeInTheDocument();

      FakeEventSource.current!.emit(
        "write_failed",
        JSON.stringify({
          type: "write_failed",
          task_id: "t-1",
          item_kind: "task",
          vault: "private-personal",
          reason: "refusing to set flag",
        }),
      );
      expect(await screen.findByTitle(/^Flag/)).toBeInTheDocument();
      expect(screen.getByText(/refusing to set flag/)).toHaveClass("toast", "error");
    });

    it("discards the 202's value when the refusal arrives before it", async () => {
      // The queue refuses the write before the browser has handled the 202,
      // so the refusal finds no pending entry yet.
      let answer202: () => void = () => undefined;
      const reads = vi.mocked(fetch).getMockImplementation()!;
      vi.mocked(fetch).mockImplementation((url, init) => {
        if (init?.method === "PATCH") {
          return new Promise((resolve) => {
            answer202 = () =>
              resolve({
                ok: true,
                status: 202,
                json: async () => ({ status: "success", task_id: "t-1", flag: true }),
              } as Response);
          });
        }
        return reads(url, init);
      });

      render(<App />);
      fireEvent.click(await screen.findByTitle(/^Flag/));
      FakeEventSource.current!.emit(
        "write_failed",
        JSON.stringify({
          type: "write_failed",
          task_id: "t-1",
          item_kind: "task",
          vault: "private-personal",
          reason: "refusing to set flag",
        }),
      );
      await act(async () => answer202());
      await act(async () => undefined);

      expect(screen.getByTitle(/^Flag/)).toBeInTheDocument();
      expect(screen.queryByTitle(/^Unflag/)).not.toBeInTheDocument();
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
