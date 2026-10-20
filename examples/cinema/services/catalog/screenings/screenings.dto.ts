import { schema, type Infer } from "@marcelsud/keel.ts";
export const screeningRef = schema.object({ screeningId: schema.string() });
export const scheduleScreening = schema.object({
  id: schema.string(),
  cinemaId: schema.string(),
  auditoriumId: schema.string(),
  movieId: schema.string(),
  startsAtMs: schema.integer(),
  basePriceMinor: schema.integer(),
  currency: schema.string(),
  holdMs: schema.integer().optional(),
  cancelCutoffMs: schema.integer().optional(),
});
export const setScreeningPrice = schema.object({
  screeningId: schema.string(),
  basePriceMinor: schema.integer(),
});
export type ScheduleScreeningDto = Infer<typeof scheduleScreening>;
export type SetScreeningPriceDto = Infer<typeof setScreeningPrice>;
