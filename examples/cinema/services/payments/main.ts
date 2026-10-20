import { connect } from "../../packages/shared-ts/index.js";
import { PaymentsModule } from "./payments.module.js";
const app = await connect("cinema-payments");
new PaymentsModule(app).register();
await app.start();
console.log("cinema-payments ready");
