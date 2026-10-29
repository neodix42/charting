package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;

/** Deliberately stalled SDK loads exercise the production dispatcher and deadlines. */
public final class BridgeResponsivenessTest {
    static final class Load {
        final Object[] args;
        Load(Object[] args) { this.args = args; }
        LoadingProgressListener progress() { return (LoadingProgressListener) args[8]; }
        void complete(boolean success) {
            long end = (Long) args[5];
            ((LoadingDataListener) args[7]).newBar((Instrument) args[0], (Period) args[1], OfferSide.BID, end, 1.1, 1.12, 1.09, 1.13, 2.5);
            progress().loadingFinished(success, end, end, end);
        }
    }
    static final class Fixture implements AutoCloseable {
        final Path cache = Files.createTempDirectory("jforex-responsive-");
        final BlockingQueue<Load> loads = new LinkedBlockingQueue<>();
        final List<JsonObject> events = new CopyOnWriteArrayList<>();
        final Set<Instrument> subscribed = ConcurrentHashMap.newKeySet();
        final AtomicReference<IStrategy> strategy = new AtomicReference<>();
        final AtomicBoolean connected = new AtomicBoolean();
        final AtomicBoolean acknowledge = new AtomicBoolean(true);
        final AtomicInteger blockingCacheReads = new AtomicInteger();
        final Bridge bridge;
        Fixture(long timeout) throws Exception {
            IHistory history = fake(IHistory.class, (method, args) -> switch (method) {
                case "getBarStart" -> Math.floorDiv((Long) args[1], ((Period) args[0]).getInterval()) * ((Period) args[0]).getInterval();
                case "getBar" -> { blockingCacheReads.incrementAndGet(); long step = ((Period) args[1]).getInterval(); yield bar(System.currentTimeMillis() / step * step, 1.1, 1.13, 1.09, 1.12); }
                case "getLastTick" -> { blockingCacheReads.incrementAndGet(); yield tick(System.currentTimeMillis(), 1.12); }
                case "getBars" -> throw new AssertionError("synchronous history must not block bridge commands");
                case "readBars" -> { check(subscribed.contains((Instrument) args[0]), "history waits for actual subscription acknowledgement"); loads.add(new Load(args)); yield null; }
                default -> null;
            });
            IDataService service = fake(IDataService.class, (method, args) -> false);
            IContext context = fake(IContext.class, (method, args) -> switch (method) {
                case "getHistory" -> history;
                case "getDataService" -> service;
                case "getSubscribedInstruments" -> new HashSet<>(subscribed);
                case "setSubscribedInstruments" -> {
                    check(Boolean.FALSE.equals(args[1]), "subscription must not wait for SDK cache loading");
                    if (acknowledge.get()) { subscribed.clear(); subscribed.addAll((Set<Instrument>) args[0]); } yield null;
                }
                default -> null;
            });
            IClient client = fake(IClient.class, (method, args) -> switch (method) {
                case "connect" -> { connected.set(true); yield null; }
                case "isConnected" -> connected.get();
                case "getAvailableInstruments" -> Set.of(Instrument.EURUSD, Instrument.USDJPY);
                case "getSubscribedInstruments" -> new HashSet<>(subscribed);
                case "unsubscribeInstruments" -> { subscribed.removeAll((Set<Instrument>) args[0]); yield null; }
                case "startStrategy" -> { strategy.set((IStrategy) args[0]); strategy.get().onStart(context); yield 1L; }
                case "disconnect" -> { connected.set(false); yield null; }
                default -> null;
            });
            bridge = new Bridge(() -> client, events::add, timeout);
            login();
        }
        void login() {
            JsonObject login = request("connect"); login.addProperty("username", "explicit-offline-user");
            login.addProperty("password", "explicit-offline-password"); login.addProperty("environment", "live"); login.addProperty("cacheDirectory", cache.toString());
            good(bridge, login);
        }
        Load load() throws Exception { Load load = loads.poll(2, TimeUnit.SECONDS); check(load != null, "SDK async history requested"); return load; }
        JsonObject subscription(String id, String period) {
            JsonObject request = request("subscribe"); request.addProperty("subscriptionId", id);
            JsonObject key = feed("EURUSD"); key.addProperty("timeframe", period); request.add("feedKey", key); return request;
        }
        void unsubscribe(String id) { JsonObject request = request("unsubscribe"); request.addProperty("subscriptionId", id); good(bridge, request); }
        JsonObject history(String id) {
            JsonObject request = request("history"); request.addProperty("id", id); request.add("feedKey", feed("EURUSD"));
            request.addProperty("limit", 3); request.addProperty("from", 0); request.addProperty("to", 0); return request;
        }
        public void close() throws Exception { bridge.close(); Files.delete(cache); }
    }

    public static void main(String[] args) throws Exception {
        try (Fixture f = new Fixture(5_000)) {
            CompletableFuture<JsonObject> first = new CompletableFuture<>(), second = new CompletableFuture<>();
            f.bridge.dispatch(f.history("one"), first::complete); Load one = f.load();
            f.bridge.dispatch(f.history("two"), second::complete); Load two = f.load();
            check(!first.isDone() && !second.isDone(), "history remains pending while control commands run");
            f.login(); check(good(f.bridge, request("instruments")).getAsJsonArray("result").size() == 2, "connection test and catalog stay responsive");
            for (String period : Bridge.INTERVALS) {
                CompletableFuture<JsonObject> ack = new CompletableFuture<>();
                f.bridge.dispatch(f.subscription(period, period), ack::complete);
                check(ack.get(1, TimeUnit.SECONDS).getAsJsonObject("result").get("subscribed").getAsBoolean(), "subscribe acknowledged independently of history for " + period);
            }
            f.strategy.get().onTick(Instrument.EURUSD, tick(System.currentTimeMillis(), 1.12));
            check(f.blockingCacheReads.get() == 0, "live callbacks must not perform potentially blocking SDK cache reads");
            check(f.events.stream().filter(event -> event.has("bars")).count() == Bridge.INTERVALS.size(), "actual cached quotes render on all periods before history completes");
            for (String period : Bridge.INTERVALS) f.unsubscribe(period);
            check(f.subscribed.contains(Instrument.EURUSD), "pending histories retain their shared instrument after chart detach");
            one.complete(true); check(first.get(2, TimeUnit.SECONDS).get("ok").getAsBoolean(), "first correlated history completes");
            check(f.subscribed.contains(Instrument.EURUSD), "second history still owns the instrument");
            two.complete(true); check(second.get(2, TimeUnit.SECONDS).get("ok").getAsBoolean(), "second correlated history completes");
            await(f.subscribed::isEmpty, "last historical request releases the instrument");
            check(f.loads.isEmpty(), "unsubscribed queued seeds never request SDK data");
        }
        try (Fixture f = new Fixture(5_000)) {
            f.acknowledge.set(false);
            good(f.bridge, f.subscription("delayed-subscription", "1m"));
            f.login(); check(f.loads.isEmpty(), "control stays responsive while SDK subscription is pending");
            f.subscribed.add(Instrument.EURUSD); Load load = f.load(); load.complete(true);
            await(() -> f.events.stream().anyMatch(event -> event.has("bars")), "acknowledged subscription loads candles asynchronously");
        }
        try (Fixture f = new Fixture(200)) {
            List<JsonObject> replies = new CopyOnWriteArrayList<>();
            f.bridge.dispatch(f.history("timeout"), replies::add); Load load = f.load();
            await(() -> replies.size() == 1, "stalled SDK history returns an explicit timeout");
            check(replies.get(0).getAsJsonObject("error").get("kind").getAsString().equals("timeout"), "timeout remains typed");
            await(() -> load.progress().stopJob(), "timed-out SDK loader receives stopJob");
            load.complete(true); check(replies.size() == 1, "late callbacks cannot duplicate a response"); f.login();
            good(f.bridge, f.subscription("seed", "1m")); Load seed = f.load();
            await(() -> f.events.stream().anyMatch(event -> event.has("health") && event.getAsJsonObject("health").get("state").getAsString().equals("error")), "stalled seed reports a visible history error");
            await(() -> seed.progress().stopJob(), "seed timeout stops the SDK loader");
            int before = f.events.size(); seed.complete(true); check(f.events.size() == before, "late seed never publishes stale bars");
            f.strategy.get().onTick(Instrument.EURUSD, tick(System.currentTimeMillis(), 1.12));
            check(f.events.get(f.events.size()-1).getAsJsonObject("health").get("state").getAsString().equals("error"), "live ticks do not hide a history failure");
            f.unsubscribe("seed"); check(f.subscribed.isEmpty(), "timed-out seed detaches cleanly");
        }
        try (Fixture f = new Fixture(5_000)) {
            good(f.bridge, f.subscription("cold-cache", "1h")); Load load = f.load();
            long start = (Long) load.args[5], previous = start - Period.ONE_HOUR.getInterval();
            f.strategy.get().onTick(Instrument.EURUSD, tick(start + 1_000, 1.20));
            f.strategy.get().onBar(Instrument.EURUSD, Period.ONE_HOUR, null, bar(previous, 1.0, 1.2, 0.9, 1.15));
            long closedRevision = f.events.get(f.events.size()-1).getAsJsonArray("bars").get(0).getAsJsonObject().get("sourceRevision").getAsLong();
            ((LoadingDataListener) load.args[7]).newBar(Instrument.EURUSD, Period.ONE_HOUR, OfferSide.BID, previous, 1.0, 1.1, 0.9, 1.2, 2.0);
            load.complete(true);
            await(() -> f.events.stream().anyMatch(event -> event.has("bars") && event.getAsJsonArray("bars").size() == 2), "history seed preserves a quote received during its load");
            JsonArray seeded = f.events.stream().filter(event -> event.has("bars") && event.getAsJsonArray("bars").size() == 2).reduce((a,b) -> b).orElseThrow().getAsJsonArray("bars");
            JsonObject active = seeded.get(1).getAsJsonObject();
            check(active.get("open").getAsDouble() == 1.1 && active.get("close").getAsDouble() == 1.20 && active.get("high").getAsDouble() == 1.20 && active.get("low").getAsDouble() == 1.09 && active.get("volume").getAsDouble() == 2.5, "seed merges actual opening context and known SDK volume without rewinding live quotes");
            check(active.get("quality").getAsString().equals("provisional"), "tick-based updates disclose incomplete current-bar volume");
            check(seeded.get(0).getAsJsonObject().get("sourceRevision").getAsLong() < closedRevision, "late history cannot supersede a newer authoritative closed bar");
            check(f.blockingCacheReads.get() == 0, "neither a seed nor a live callback loads synchronous candle/tick caches");
            f.strategy.get().onBar(Instrument.EURUSD, Period.ONE_HOUR, null, bar(start, 1.1, 1.21, 1.09, 1.20));
            int before = f.events.size(); f.strategy.get().onTick(Instrument.EURUSD, tick(start + 2_000, 1.19));
            check(f.events.size() == before, "a delayed tick cannot reopen an SDK-closed candle");
        }
        try (Fixture f = new Fixture(5_000)) {
            good(f.bridge, f.subscription("cancel", "1h")); Load load = f.load(); f.unsubscribe("cancel");
            await(() -> load.progress().stopJob(), "unsubscribe cancels an active asynchronous load");
            int before = f.events.size(); load.complete(true); check(f.events.size() == before, "unsubscribed callback cannot publish bars or health");
            good(f.bridge, f.subscription("failed", "1d")); Load failed = f.load(); failed.complete(false);
            await(() -> f.events.stream().anyMatch(event -> event.has("health") && event.getAsJsonObject("health").get("state").getAsString().equals("error")), "partial history failure is visible, never treated as complete");
        }
        System.out.println("JForex responsive control, all-period ticks, deadlines and cancellation checks passed");
    }
}
