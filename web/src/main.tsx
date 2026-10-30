import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { adoptToken } from "./api";
import { App } from "./App";
import "./styles.css";

adoptToken();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
