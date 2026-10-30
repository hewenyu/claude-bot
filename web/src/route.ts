// Routes live in the URL fragment: #/p/3, #/p/3/t/<task>, #/p/3/channels,
// #/settings. The server serves the page for every path anyway.

import { useEffect, useState } from "react";

export type Route =
  | { page: "home" }
  | { page: "settings" }
  | { page: "project"; project: number; tab: "tasks" | "channels"; task?: string };

export function parse(hash: string): Route {
  const parts = hash.replace(/^#\/?/, "").split("/").filter(Boolean).map(decodeURIComponent);

  if (parts[0] === "settings") return { page: "settings" };

  if (parts[0] === "p" && /^\d+$/.test(parts[1] ?? "")) {
    const project = Number(parts[1]);
    if (parts[2] === "channels") return { page: "project", project, tab: "channels" };
    if (parts[2] === "t" && parts[3]) return { page: "project", project, tab: "tasks", task: parts[3] };

    return { page: "project", project, tab: "tasks" };
  }

  return { page: "home" };
}

export function href(r: Route): string {
  switch (r.page) {
    case "home":
      return "#/";
    case "settings":
      return "#/settings";
    case "project":
      if (r.tab === "channels") return `#/p/${r.project}/channels`;
      if (r.task) return `#/p/${r.project}/t/${encodeURIComponent(r.task)}`;

      return `#/p/${r.project}`;
  }
}

export function go(r: Route): void {
  location.hash = href(r);
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parse(location.hash));

  useEffect(() => {
    const on = () => setRoute(parse(location.hash));
    window.addEventListener("hashchange", on);

    return () => window.removeEventListener("hashchange", on);
  }, []);

  return route;
}
