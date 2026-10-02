// Entry module of the extension's frontend (opskat.Frontend in main.go). The host
// injects window.__OPSKAT_EXT__ before importing it, so registering the page's
// locales at import time is safe.
import "./styles.css";
import { registerLocales } from "./i18n";

registerLocales();

export { ElasticsearchPage } from "./components/ElasticsearchPage";
