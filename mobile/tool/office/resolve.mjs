// Lets Node run the web's TypeScript as it stands: its imports carry no
// extension ("./mathe"), which Node's own type stripping does not resolve.
import { register } from "node:module";

register(
  "data:text/javascript," +
    encodeURIComponent(`
export async function resolve(spec, ctx, next) {
  try { return await next(spec, ctx); }
  catch (e) {
    if (spec.startsWith(".") && !/\\.[cm]?[jt]s$/.test(spec)) return next(spec + ".ts", ctx);
    throw e;
  }
}`),
);
