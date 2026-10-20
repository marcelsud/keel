import { type Keel } from "@marcelsud/keel.ts";
import { CatalogService } from "./catalog.service.js";
export class CatalogController {
  constructor(private readonly service: CatalogService) {}
  register(app: Keel): void {
    app.action({
      name: "list",
      args: app.schema.object({}),
      handler: () => this.service.list(),
    });
  }
}
