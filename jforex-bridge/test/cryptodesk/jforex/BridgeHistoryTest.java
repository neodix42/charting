package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.google.gson.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import static cryptodesk.jforex.BridgeTest.*;
import static cryptodesk.jforex.BridgeResponsivenessTest.*;

/** Offline regressions for closed history publication independent of current candles. */
public final class BridgeHistoryTest {
    static final long NOW = Instant.parse("2026-10-28T12:34:00Z").toEpochMilli();
    static long current(String interval) {
        return Instant.parse(interval.equals("1w") ? "2026-10-26T00:00:00Z" : "2026-10-28T00:00:00Z").toEpochMilli();
    }
    static List<JsonObject> barEvents(Fixture f) {
        return f.events.stream().filter(event -> event.has("bars")).toList();
    }
    static boolean state(Fixture f, String expected) {
        return f.events.stream().anyMatch(event -> event.has("health") && event.getAsJsonObject("health").get("state").getAsString().equals(expected));
    }
    static JsonObject history(Fixture f, String interval, long from, long to, int limit) {
        JsonObject query = f.history("historical-window");
        query.getAsJsonObject("feedKey").addProperty("timeframe", interval);
        query.addProperty("from", from / 1000); query.addProperty("to", to / 1000); query.addProperty("limit", limit);
        return query;
    }

    public static void main(String[] args) throws Exception {
        for (String interval : List.of("1d", "1w")) {
            Period period = Bridge.PERIODS.get(interval); long step = period.getInterval(), current = current(interval);
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                good(f.bridge, f.subscription("cold-long-period", interval)); Load closed = f.load();
                check(!closed.range() && (Integer) closed.args[4] == 499 && closed.end() == current - step,
                    "cold " + interval + " history excludes the unfinished current candle");
                closed.emit(current - 2 * step); closed.complete(true); Load active = f.load();
                check(active.range() && active.from() == current && active.end() == current,
                    "current " + interval + " uses one native candle request");
                List<JsonObject> published = barEvents(f);
                check(published.size() == 1 && published.get(0).getAsJsonArray("bars").size() == 2,
                    "closed " + interval + " candles publish before a stalled current snapshot");
                JsonObject checkpoint = published.get(0);
                check(checkpoint.get("coverageFrom").getAsLong() == (current - 2 * step) / 1000
                    && checkpoint.get("coverageTo").getAsLong() == current / 1000,
                    "closed history checkpoint excludes the stalled unfinished candle");
                check(checkpoint.getAsJsonArray("bars").asList().stream().allMatch(value -> value.getAsJsonObject().get("closed").getAsBoolean()),
                    "completed history candles retain authoritative closure");
                f.strategy.get().onTick(Instrument.EURUSD, tick(NOW, 1.22));
                check(!state(f, "live") && state(f, "reconciling"), "quotes cannot hide pending current candle context");
                active.complete(true);
                await(() -> state(f, "live"), "native current candle completes the chart's historical reconciliation");
                JsonObject last = barEvents(f).get(barEvents(f).size() - 1).getAsJsonArray("bars").get(0).getAsJsonObject();
                check(last.get("time").getAsLong() == current / 1000 && last.get("open").getAsDouble() == 1.1
                    && last.get("close").getAsDouble() == 1.22 && last.get("high").getAsDouble() == 1.22
                    && last.get("low").getAsDouble() == 1.09 && last.get("volume").getAsDouble() == 2.5,
                    "native current snapshot restores opening context/known volume while retaining the latest live quote");
                check(!last.get("closed").getAsBoolean() && last.get("quality").getAsString().equals("provisional"),
                    "current snapshot plus quotes remains provisional");
                check(last.get("sourceRevision").getAsLong() > checkpoint.getAsJsonArray("bars").get(0).getAsJsonObject().get("sourceRevision").getAsLong(),
                    "merged current quote has a newer source revision than closed seed history");
                check(f.blockingCacheReads.get() == 0, "daily/weekly seeds do not use synchronous candle or tick getters");
            }
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                JsonObject request = f.subscription("warm-long-period", interval);
                request.addProperty("historyFrom", (current - 2 * step) / 1000);
                good(f.bridge, request); Load closed = f.load();
                check(closed.range() && closed.from() == current - 2 * step && closed.end() == current - step,
                    "warm " + interval + " catch-up fetches only closed missing overlap");
                closed.complete(true); Load active = f.load();
                check(barEvents(f).get(0).get("coverageTo").getAsLong() == current / 1000,
                    "warm completed historical range is durable before current snapshot finishes");
                active.finish(false);
                await(() -> state(f, "error"), "current snapshot failure stays visible");
                check(!state(f, "live") && barEvents(f).size() == 1,
                    "failed current snapshot cannot mark the chart live or discard its completed history");
                f.strategy.get().onTick(Instrument.EURUSD, tick(NOW, 1.24));
                check(f.events.get(f.events.size() - 1).getAsJsonObject("health").get("state").getAsString().equals("error"),
                    "quotes preserve a current-snapshot failure");
                f.listener.get().onConnect(); Load retry = f.load();
                check(retry.range() && retry.from() == current - step && retry.end() == current - step,
                    "reconnect reuses the proven historical checkpoint after a failed current snapshot");
                retry.finish(true); Load canceled = f.load(); f.unsubscribe("warm-long-period");
                await(() -> canceled.progress().stopJob(), "detach cancels pending current native history");
                int before = f.events.size(); canceled.complete(true);
                check(f.events.size() == before, "a canceled current snapshot cannot publish stale history or health");
            }
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                CompletableFuture<JsonObject> reply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current - 3 * step, current, 500), reply::complete); Load closed = f.load();
                check(closed.range() && closed.from() == current - 3 * step && closed.end() == current - step,
                    "narrow historical " + interval + " gap does not reload 500 old candles");
                closed.emit(current - 3 * step); closed.emit(current - step); closed.finish(true);
                JsonArray result = reply.get(2, TimeUnit.SECONDS).getAsJsonArray("result");
                check(result.size() == 2 && result.get(0).getAsJsonObject().get("time").getAsLong() == (current - 3 * step) / 1000
                    && result.get(1).getAsJsonObject().get("time").getAsLong() == (current - step) / 1000,
                    "closed historical query returns ordered real bars and preserves empty market gaps");
                check(f.loads.isEmpty(), "past-only pagination never requests a current snapshot");
                CompletableFuture<JsonObject> recentReply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current - 2 * step, NOW + 1000, 2), recentReply::complete);
                Load recent = f.load();
                check(recent.range() && recent.from() == current - 2 * step && recent.end() == current - step,
                    "recent page loads its closed historical portion without the current candle");
                recent.emit(current - 2 * step); recent.complete(true); Load currentLoad = f.load();
                check(currentLoad.range() && currentLoad.from() == current && currentLoad.end() == current && !recentReply.isDone(),
                    "recent historical RPC obtains native current data independently and awaits both pieces");
                currentLoad.complete(true);
                JsonArray latest = recentReply.get(2, TimeUnit.SECONDS).getAsJsonArray("result");
                check(latest.size() == 2 && latest.get(0).getAsJsonObject().get("time").getAsLong() == (current - step) / 1000
                    && latest.get(1).getAsJsonObject().get("time").getAsLong() == current / 1000,
                    "history keeps the newest requested limit across closed/current merge");
                check(latest.get(0).getAsJsonObject().get("closed").getAsBoolean()
                    && !latest.get(1).getAsJsonObject().get("closed").getAsBoolean(),
                    "historical/current RPC merge preserves authoritative/provisional boundary");
                CompletableFuture<JsonObject> currentReply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current, NOW + 1000, 1), currentReply::complete);
                Load onlyCurrent = f.load();
                check(onlyCurrent.range() && onlyCurrent.from() == current && onlyCurrent.end() == current,
                    "a current-only history request skips the entire closed-history page");
                onlyCurrent.complete(true);
                check(currentReply.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 1 && f.loads.isEmpty(),
                    "a current-only history request uses exactly one native snapshot");
            }
            AtomicLong rolloverClock = new AtomicLong(current + step - 1_000);
            try (Fixture f = new Fixture(5_000, rolloverClock::get)) {
                good(f.bridge, f.subscription("rollover", interval)); Load closed = f.load();
                closed.complete(true); Load active = f.load();
                check(active.range() && active.from() == current && active.end() == current,
                    interval + " rollover starts with the actual in-progress candle");
                // The SDK delivers a forming snapshot before the boundary, then
                // finishes the asynchronous request after that candle has closed.
                ((LoadingDataListener) active.args[active.args.length - 2]).newBar(Instrument.EURUSD, period,
                    OfferSide.BID, current, 1.01, 1.03, 0.99, 1.04, 1.5);
                rolloverClock.set(current + step + 1_000); active.finish(true);
                Load finalized = f.load();
                check(finalized.range() && finalized.from() == current && finalized.end() == current,
                    interval + " rollover refetches the now-closed candle before claiming it authoritative");
                check(barEvents(f).size() == 1 && barEvents(f).get(0).get("coverageTo").getAsLong() == current / 1000 && !state(f, "live"),
                    "a pre-close snapshot finishing after rollover cannot publish closed data, advance coverage or finish reconciliation");
                ((LoadingDataListener) finalized.args[finalized.args.length - 2]).newBar(Instrument.EURUSD, period,
                    OfferSide.BID, current, 1.01, 1.18, 0.98, 1.25, 4.5);
                finalized.finish(true); Load nextCurrent = f.load();
                List<JsonObject> afterClose = barEvents(f);
                JsonObject finalizedEvent = afterClose.get(afterClose.size() - 1);
                JsonObject finalizedBar = finalizedEvent.getAsJsonArray("bars").get(0).getAsJsonObject();
                check(finalizedBar.get("time").getAsLong() == current / 1000 && finalizedBar.get("close").getAsDouble() == 1.18
                    && finalizedBar.get("high").getAsDouble() == 1.25 && finalizedBar.get("low").getAsDouble() == 0.98
                    && finalizedBar.get("volume").getAsDouble() == 4.5 && finalizedBar.get("closed").getAsBoolean()
                    && finalizedBar.get("quality").getAsString().equals("authoritative"),
                    "rollover publishes the refetched final native values rather than promoting the older forming snapshot");
                check(finalizedEvent.get("coverageFrom").getAsLong() == current / 1000
                    && finalizedEvent.get("coverageTo").getAsLong() == (current + step) / 1000 && !state(f, "live"),
                    "only successfully refetched closed history advances the rollover checkpoint");
                check(nextCurrent.range() && nextCurrent.from() == current + step && nextCurrent.end() == current + step,
                    "rollover requests the new native current candle at its correct daily or Monday weekly boundary");
                f.strategy.get().onTick(Instrument.EURUSD, tick(rolloverClock.get(), 1.30));
                nextCurrent.complete(true);
                await(() -> state(f, "live"), "rollover reconciliation finishes only after the new current snapshot succeeds");
                List<JsonObject> finished = barEvents(f);
                JsonObject newCurrent = finished.get(finished.size() - 1).getAsJsonArray("bars").get(0).getAsJsonObject();
                check(newCurrent.get("time").getAsLong() == (current + step) / 1000 && !newCurrent.get("closed").getAsBoolean()
                    && newCurrent.get("open").getAsDouble() == 1.1 && newCurrent.get("close").getAsDouble() == 1.30,
                    "the newly opened rollover candle preserves real opening context and the newer live quote");
                check(f.blockingCacheReads.get() == 0, "rollover recovery never blocks on synchronous candle or tick cache getters");
            }
            AtomicLong rpcClock = new AtomicLong(current + step - 1_000);
            try (Fixture f = new Fixture(5_000, rpcClock::get)) {
                CompletableFuture<JsonObject> reply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, current, current + step, 1), reply::complete);
                Load active = f.load();
                check(active.range() && active.from() == current && active.end() == current,
                    "current-only " + interval + " RPC initially requests the forming native candle");
                ((LoadingDataListener) active.args[active.args.length - 2]).newBar(Instrument.EURUSD, period,
                    OfferSide.BID, current, 1.01, 1.03, 0.99, 1.04, 1.5);
                rpcClock.set(current + step + 1_000); active.finish(true);
                Load finalized = f.load();
                check(finalized.range() && finalized.from() == current && finalized.end() == current && !reply.isDone(),
                    "historical RPC refetches a newly closed requested candle instead of certifying its pre-close snapshot");
                ((LoadingDataListener) finalized.args[finalized.args.length - 2]).newBar(Instrument.EURUSD, period,
                    OfferSide.BID, current, 1.01, 1.18, 0.98, 1.25, 4.5);
                finalized.finish(true);
                JsonArray result = reply.get(2, TimeUnit.SECONDS).getAsJsonArray("result");
                check(result.size() == 1, "current-only RPC rollover preserves the requested row limit");
                JsonObject finalBar = result.get(0).getAsJsonObject();
                check(finalBar.get("time").getAsLong() == current / 1000 && finalBar.get("open").getAsDouble() == 1.01
                    && finalBar.get("close").getAsDouble() == 1.18 && finalBar.get("high").getAsDouble() == 1.25
                    && finalBar.get("low").getAsDouble() == 0.98 && finalBar.get("volume").getAsDouble() == 4.5
                    && finalBar.get("closed").getAsBoolean() && finalBar.get("quality").getAsString().equals("authoritative"),
                    "RPC rollover returns refetched final native OHLCV with truthful closure");
                check(f.loads.isEmpty(), "exclusive historical end at the rollover boundary never requests the next current candle");
                check(f.blockingCacheReads.get() == 0, "RPC rollover uses only asynchronous native history");
            }
        }
        try (Fixture f = new Fixture(400, () -> NOW)) {
            long current = current("1d");
            good(f.bridge, f.subscription("current-timeout", "1d")); Load closed = f.load();
            closed.complete(true); Load active = f.load();
            await(() -> state(f, "error"), "a stalled native current snapshot hits the bounded history deadline");
            await(() -> active.progress().stopJob(), "current-snapshot timeout cancels the native loader");
            check(barEvents(f).size() == 1 && barEvents(f).get(0).get("coverageTo").getAsLong() == current / 1000 && !state(f, "live"),
                "current timeout preserves published closed history and its checkpoint without claiming live reconciliation");
            int before = f.events.size(); active.complete(true);
            check(f.events.size() == before, "timed-out current data cannot publish late");
        }
        for (String interval : List.of("1m", "1h", "4h")) {
            long step = Bridge.PERIODS.get(interval).getInterval(), end = Math.floorDiv(NOW, step) * step;
            try (Fixture f = new Fixture(5_000, () -> NOW)) {
                CompletableFuture<JsonObject> reply = new CompletableFuture<>();
                f.bridge.dispatch(history(f, interval, end - 2 * step, end, 500), reply::complete); Load gap = f.load();
                check(gap.range() && gap.from() == end - 2 * step && gap.end() == end - step,
                    "narrow " + interval + " historical repair also respects the missing range instead of the page limit");
                gap.complete(true);
                check(reply.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 1 && f.loads.isEmpty(),
                    "intraday narrow history completes without unrelated data requests");
            }
        }
        System.out.println("JForex daily/weekly closed-history-first, narrow backfill, current snapshot, checkpoints and cancellation checks passed");
    }
}
