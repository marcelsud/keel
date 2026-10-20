export type Seat = {
  id: string;
  category: string;
  blocked: boolean;
  priceMinor: number;
};
export type Movie = { id: string; title: string; durationMs: number };
export type Cinema = {
  id: string;
  name: string;
  auditoriums: {
    id: string;
    seats: { id: string; category: string; blocked: boolean }[];
  }[];
};
export type Screening = {
  id: string;
  cinemaId: string;
  auditoriumId: string;
  movieId: string;
  startsAtMs: number;
  endsAtMs: number;
  basePriceMinor: number;
  currency: string;
  holdMs: number;
  cancelCutoffMs: number;
  canceled: boolean;
  seats: Seat[];
};
export type BookingEvent = {
  id: string;
  kind: string;
  screeningId: string;
  orderId: string;
  customerId: string;
  status: string;
  refundStatus: string;
};
