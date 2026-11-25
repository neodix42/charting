package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.google.gson.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;
import static cryptodesk.jforex.BridgeResponsivenessTest.*;

/** Download-only blocking server-loaded SDK collections, independent of chart callbacks. */
public final class BridgeNativeDownloadTest {
    static final long FROM = java.time.Instant.parse("2024-01-01T00:00:00Z").getEpochSecond();
    static final long TO = FROM + 3 * 86_400;
    static final class NativeLoad {
        final Object[] args;
        final CompletableFuture<List<IBar>> result = new CompletableFuture<>();
        final AtomicBoolean canceled = new AtomicBoolean();
        NativeLoad(Object[] args) { this.args = args; }
        void complete() {
            check(args.length == 6 && args[0] == Instrument.BTCUSD && args[1] == Period.DAILY && args[2] == OfferSide.BID && args[3] == Filter.WEEKENDS
                && (Long) args[4] == FROM * 1000 && (Long) args[5] == (TO - 86_400) * 1000,
                "native getBars uses exact SDK native identity, BID, weekend filtering, first/last closed bar starts");
            result.complete(List.of(bar(FROM * 1000, 1, 3, .5, 2), bar((FROM + 86_400) * 1000, 2, 4, 1, 3), bar((TO - 86_400) * 1000, 3, 5, 2, 4)));
        }
    }
    static final class NativeFixture implements AutoCloseable {
        final BlockingQueue<NativeLoad> calls = new LinkedBlockingQueue<>();
        final Fixture charts;
        final Bridge bridge;
        NativeFixture(long chartTimeout, long downloadTimeout) throws Exception {
            charts = new Fixture(chartTimeout, () -> (TO + 3_600) * 1000, 2_000, downloadTimeout, Set.of(Instrument.BTCUSD, Instrument.EURUSD), (method, args) -> {
                NativeLoad call = new NativeLoad(args); calls.add(call);
                try { return call.result.get(); }
                catch (InterruptedException error) { call.canceled.set(true); throw error; }
            });
            bridge = charts.bridge;
        }
        NativeLoad load() throws Exception { NativeLoad load = calls.poll(2, TimeUnit.SECONDS); check(load != null, "blocking SDK getBars requested only for native download"); return load; }
        public void close() throws Exception { charts.close(); }
    }
    static JsonObject history(boolean download) {
        JsonObject request = request("history"), key = feed(Instrument.BTCUSD, "cfd"); key.addProperty("timeframe", "1d");
        request.add("feedKey", key); request.addProperty("from", FROM); request.addProperty("to", TO); request.addProperty("limit", 1000);
        if (download) request.addProperty("download", true); return request;
    }
    static CompletableFuture<JsonObject> dispatch(NativeFixture f, JsonObject request) {
        CompletableFuture<JsonObject> reply = new CompletableFuture<>(); f.bridge.dispatch(request, reply::complete); return reply;
    }
    static void failed(JsonObject response, String message) { check(!response.get("ok").getAsBoolean(), message + ": " + response); }
    public static void main(String[] args) throws Exception {
        try (NativeFixture f = new NativeFixture(75, 2_000)) {
            CompletableFuture<JsonObject> nativeReply = dispatch(f, history(true)); NativeLoad nativeLoad = f.load();
            CompletableFuture<JsonObject> chartReply = dispatch(f, history(false)); Load chartLoad = f.charts.load();
            JsonObject chart = chartReply.get(2, TimeUnit.SECONDS); failed(chart, "ordinary chart history retains its short deadline");
            check(chart.getAsJsonObject("error").get("message").getAsString().contains("historical data loading timed out") && !nativeReply.isDone(),
                "blocking server-loaded collection survives chart deadline without relying on background cache callbacks");
            await(() -> chartLoad.progress().stopJob(), "chart deadline still cancels its own async SDK loader");
            nativeLoad.complete(); JsonObject result = nativeReply.get(2, TimeUnit.SECONDS);
            check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() == 3
                && result.getAsJsonArray("result").get(2).getAsJsonObject().get("close").getAsDouble() == 4,
                "slow native daily load publishes the exact server-loaded closed OHLC collection");
            check(result.getAsJsonArray("result").asList().stream().allMatch(value -> value.getAsJsonObject().get("closed").getAsBoolean()), "download cannot publish unfinished candles");
            await(f.charts.subscribed::isEmpty, "chart and download release separate subscription references");
            CompletableFuture<JsonObject> nextChart = dispatch(f, history(false)); f.charts.load();
            JsonObject chartAgain = nextChart.get(2, TimeUnit.SECONDS); failed(chartAgain, "download API and budget never leak to a reused chart worker");
            check(f.calls.isEmpty() && !chartAgain.getAsJsonObject("error").get("message").getAsString().contains("native daily download"), "chart remains on its independent async API");
        }
        // Model the observed cold-cache symptom: async background completion
        // reports no callback rows while the authoritative loaded collection is
        // still pending. The download must await getBars rather than publish [].
        try (NativeFixture f = new NativeFixture(1_000, 2_000)) {
            CompletableFuture<JsonObject> download = dispatch(f, history(true)); NativeLoad server = f.load();
            CompletableFuture<JsonObject> chart = dispatch(f, history(false)); f.charts.load().finish(true);
            check(chart.get(2, TimeUnit.SECONDS).getAsJsonArray("result").isEmpty() && !download.isDone(), "empty background callbacks do not certify a pending server-loaded download");
            server.complete(); check(download.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 3 && f.charts.loads.isEmpty(), "native download uses only its actual getBars collection");
        }
        for (String invalid : List.of("instrument", "market", "timeframe", "open", "alignment", "range", "limit", "flag")) {
            try (NativeFixture f = new NativeFixture(50, 1_000)) {
                JsonObject request = history(true), key = request.getAsJsonObject("feedKey");
                switch (invalid) {
                    case "instrument" -> { key.addProperty("instrument", "EURUSD"); key.addProperty("market", "forex"); }
                    case "market" -> key.addProperty("market", "metal");
                    case "timeframe" -> key.addProperty("timeframe", "1w");
                    case "open" -> request.addProperty("to", TO + 86_400);
                    case "alignment" -> request.addProperty("from", FROM + 1);
                    case "range" -> request.addProperty("from", TO - 367 * 86_400);
                    case "limit" -> request.addProperty("limit", 1001);
                    case "flag" -> request.addProperty("download", "true");
                }
                failed(dispatch(f, request).get(1, TimeUnit.SECONDS), "invalid download scope " + invalid);
                check(f.calls.isEmpty() && f.charts.loads.isEmpty(), "invalid longer-deadline request never reaches either SDK API");
            }
        }
        for (String kind : List.of("empty", "null", "oversized", "price", "unaligned", "outside", "duplicate", "order", "failure")) {
            try (NativeFixture f = new NativeFixture(50, 1_000)) {
                CompletableFuture<JsonObject> download = dispatch(f, history(true)); NativeLoad load = f.load();
                switch (kind) {
                    case "empty" -> load.result.complete(List.of());
                    case "null" -> load.result.complete(null);
                    case "oversized" -> load.result.complete(Collections.nCopies(367, bar(FROM * 1000, 1, 2, .5, 1.5)));
                    case "price" -> load.result.complete(List.of(bar(FROM * 1000, -1, 2, .5, 1.5)));
                    case "unaligned" -> load.result.complete(List.of(bar(FROM * 1000 + 1, 1, 2, .5, 1.5)));
                    case "outside" -> load.result.complete(List.of(bar(TO * 1000, 1, 2, .5, 1.5)));
                    case "duplicate" -> load.result.complete(List.of(bar(FROM * 1000, 1, 2, .5, 1.5), bar(FROM * 1000, 1, 2, .5, 1.5)));
                    case "order" -> load.result.complete(List.of(bar((FROM + 86_400) * 1000, 1, 2, .5, 1.5), bar(FROM * 1000, 1, 2, .5, 1.5)));
                    case "failure" -> load.result.completeExceptionally(new JFException("explicit offline source failure"));
                }
                JsonObject result = download.get(2, TimeUnit.SECONDS);
                if (kind.equals("empty")) check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").isEmpty(), "genuine loaded server-empty range remains empty with no fabricated OHLC");
                else failed(result, "invalid loaded collection cannot publish " + kind);
                await(f.charts.subscribed::isEmpty, "collection success/failure releases native subscription");
            }
        }
        try (NativeFixture f = new NativeFixture(50, 200)) {
            CompletableFuture<JsonObject> stalled = dispatch(f, history(true)); NativeLoad first = f.load();
            JsonObject failure = stalled.get(2, TimeUnit.SECONDS); failed(failure, "blocking native download remains bounded");
            check(failure.getAsJsonObject("error").get("kind").getAsString().equals("timeout")
                && failure.getAsJsonObject("error").get("message").getAsString().equals("JForex native daily download timed out. Completed history is retained; retry to resume."), "native deadline has exact retryable scope");
            await(first.canceled::get, "native deadline interrupts its bounded SDK collection worker");
            CompletableFuture<JsonObject> resumed = dispatch(f, history(true)); f.load().complete();
            check(resumed.get(2, TimeUnit.SECONDS).get("ok").getAsBoolean() && f.charts.connected.get(), "explicit retry reuses healthy SDK session");
        }
        try (NativeFixture f = new NativeFixture(50, 250)) {
            CompletableFuture<JsonObject> first = dispatch(f, history(true)); f.load();
            CompletableFuture<JsonObject> second = dispatch(f, history(true)); f.load();
            CompletableFuture<JsonObject> queued = dispatch(f, history(true));
            failed(first.get(2, TimeUnit.SECONDS), "first native worker remains bounded"); failed(second.get(2, TimeUnit.SECONDS), "second native worker remains bounded");
            NativeLoad queuedLoad = f.load();
            CompletableFuture<JsonObject> clock = dispatch(f, history(false)); f.charts.load(); failed(clock.get(2, TimeUnit.SECONDS), "short chart deadline advances after queued work starts");
            check(!queued.isDone(), "queued native work receives its full SDK budget at executor entry");
            queuedLoad.complete(); check(queued.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() == 3, "queued native collection succeeds exactly once");
        }
        System.out.println("JForex native loaded getBars collections, scoped download deadlines, genuine empty ranges, validation, queueing and chart isolation checks passed");
    }
}
