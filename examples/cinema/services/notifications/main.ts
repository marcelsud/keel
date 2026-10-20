import { connect } from "../../packages/shared-ts/index.js";
import { NotificationsModule } from "./notifications.module.js";
const app = await connect("cinema-notifications");
new NotificationsModule(app).register();
await app.start();
console.log("cinema-notifications ready");
