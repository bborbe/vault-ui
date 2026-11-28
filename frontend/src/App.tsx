import { useEffect, useState } from "react";
import { fetchTasks, subscribeBoard } from "./api";
import type { TaskResponse } from "./types";

// App is the scaffold shell: it proves the transport seam, the SSE subscription
// and the error path end to end. The board itself lands view by view on top of
// this, against the parity checklist in the owning task.
export default function App() {
  const [tasks, setTasks] = useState<TaskResponse[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const load = () =>
      fetchTasks({}, controller.signal)
        .then((next) => {
          setTasks(next);
          setError(null);
        })
        .catch((err: Error) => {
          if (err.name !== "AbortError") setError(err.message);
        });
    load();
    const unsubscribe = subscribeBoard(load);
    return () => {
      unsubscribe();
      controller.abort();
    };
  }, []);

  if (error) {
    return <p className="err">Could not read /api/tasks: {error}</p>;
  }
  if (!tasks) {
    return <p className="subtitle">Loading…</p>;
  }
  return (
    <ul>
      {tasks.map((task) => (
        <li key={task.id} className="task-card" data-task-id={task.title}>
          {task.title}
        </li>
      ))}
    </ul>
  );
}
