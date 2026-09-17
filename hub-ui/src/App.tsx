import { BrowserRouter } from "react-router-dom";
import { Application } from "./app/auth/Application";

export function App() {
  return <BrowserRouter><Application /></BrowserRouter>;
}
