import { connect } from "../../packages/shared-ts/index.js";
import { CatalogModule } from "./catalog.module.js";
const app = await connect("cinema-catalog");
await new CatalogModule(app).register();
await app.start();
console.log("cinema-catalog ready");
