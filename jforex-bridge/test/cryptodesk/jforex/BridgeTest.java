package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.system.*;
import com.google.gson.*;
import java.awt.image.BufferedImage;
import java.lang.reflect.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.BooleanSupplier;

/** Explicit offline SDK-interface fakes exercise production protocol and lifecycle. */
public final class BridgeTest {
    interface Call { Object invoke(String method, Object[] args) throws Throwable; }
    static <T> T fake(Class<T> type, Call call) {
        return type.cast(Proxy.newProxyInstance(type.getClassLoader(), new Class<?>[]{type}, (proxy, method, args) -> {
            if (method.getName().equals("toString")) return "explicit-offline-fake";
            if (method.getName().equals("hashCode")) return System.identityHashCode(proxy);
            if (method.getName().equals("equals")) return proxy == args[0];
            Object value = call.invoke(method.getName(), args == null ? new Object[0] : args);
            if (value != null || !method.getReturnType().isPrimitive()) return value;
            if (method.getReturnType() == boolean.class) return false;
            if (method.getReturnType() == long.class) return 0L;
            if (method.getReturnType() == int.class) return 0;
            if (method.getReturnType() == double.class) return 0.0;
            return null;
        }));
    }
    static IBar bar(long time, double open, double high, double low, double close) {
        return fake(IBar.class, (method, args) -> switch (method) {
            case "getTime" -> time; case "getOpen" -> open; case "getHigh" -> high; case "getLow" -> low;
            case "getClose" -> close; case "getVolume" -> 2.5; default -> null;
        });
    }
    static ITick tick(long time, double bid) {
        return fake(ITick.class, (method, args) -> switch (method) { case "getTime" -> time; case "getBid" -> bid; default -> null; });
    }
    static void check(boolean value, String message) { if (!value) throw new AssertionError(message); }
    static void await(BooleanSupplier ready, String message) throws Exception {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
        while (!ready.getAsBoolean() && System.nanoTime() < deadline) Thread.sleep(5);
        check(ready.getAsBoolean(), message);
    }
    static JsonObject request(String op) { JsonObject out = new JsonObject(); out.addProperty("id", "test-" + op); out.addProperty("op", op); return out; }
    static JsonObject feed(String instrument) { return JsonParser.parseString("{\"exchange\":\"dukascopy\",\"market\":\"forex\",\"instrument\":\"" + instrument + "\",\"timeframe\":\"1m\"}").getAsJsonObject(); }
    static JsonObject feed(Instrument instrument, String market) { JsonObject out = feed(instrument.name()); out.addProperty("market", market); return out; }
    static JsonObject good(Bridge bridge, JsonObject request) { JsonObject response = bridge.handle(request); check(response.get("ok").getAsBoolean(), response.toString()); return response; }
    public static void main(String[] args) throws Exception {
        Path cache = Files.createTempDirectory("cryptodesk-jforex-test-");
        List<JsonObject> events = Collections.synchronizedList(new ArrayList<>());
        AtomicBoolean connected = new AtomicBoolean(), offline = new AtomicBoolean();
        Set<Instrument> subscribed = ConcurrentHashMap.newKeySet();
        AtomicInteger factories = new AtomicInteger(), historyCalls = new AtomicInteger(), authArgs = new AtomicInteger();
        AtomicReference<Object[]> historyArguments = new AtomicReference<>();
        AtomicReference<IStrategy> strategy = new AtomicReference<>();
        Map<Instrument, String> offered = new HashMap<>(Map.of(Instrument.EURUSD, "forex", Instrument.USDJPY, "forex",
            Instrument.BTCUSD, "cfd", Instrument.ETHUSD, "cfd", Instrument.XAUUSD, "metal", Instrument.XAGUSD, "metal",
            Instrument.AAPLUSUSD, "cfd", Instrument.USA30IDXUSD, "cfd", Instrument.BRENTCMDUSD, "cfd", Instrument.SPYUSUSD, "cfd"));
        Instrument offshoreYuan = Instrument.fromString("USD/CNH");
        check(offshoreYuan != null && offshoreYuan.getSecondaryJFCurrency().getJavaCurrency() == null,
            "regression covers SDK fiat absent from Java ISO currencies");
        offered.put(offshoreYuan, "forex");
        long now = System.currentTimeMillis() / 60000 * 60000;
        AtomicReference<IBar> active = new AtomicReference<>(bar(now, 1.10, 1.15, 1.09, 1.14));
        IHistory history = fake(IHistory.class, (method, values) -> switch (method) {
            case "getBarStart" -> ((Long) values[1]) / 60000 * 60000;
            case "getBar" -> active.get();
            case "getLastTick" -> tick(now + 500, 1.14);
            case "getBars" -> throw new AssertionError("blocking historical SDK method must not be used");
            case "readBars" -> {
                historyCalls.incrementAndGet(); historyArguments.set(values);
                LoadingDataListener listener = (LoadingDataListener) values[7];
                for (IBar item : List.of(bar(now - 120000, 1.07, 1.10, 1.06, 1.08), bar(now - 60000, 1.08, 1.12, 1.07, 1.10), active.get()))
                    listener.newBar((Instrument) values[0], (Period) values[1], OfferSide.BID, item.getTime(), item.getOpen(), item.getClose(), item.getLow(), item.getHigh(), item.getVolume());
                ((LoadingProgressListener) values[8]).loadingFinished(true, now - 120000, now, now);
                yield null;
            }
            default -> null;
        });
        IDataService dataService = fake(IDataService.class, (method, values) -> method.equals("isOfflineTime") ? offline.get() : null);
        IContext context = fake(IContext.class, (method, values) -> switch (method) {
            case "getHistory" -> history; case "getDataService" -> dataService;
            case "getSubscribedInstruments" -> new HashSet<>(subscribed);
            case "setSubscribedInstruments" -> { check(values.length == 2 && Boolean.FALSE.equals(values[1]), "control subscriptions must not wait for history"); subscribed.clear(); subscribed.addAll((Set<Instrument>) values[0]); yield null; }
            default -> null;
        });
        IClient client = fake(IClient.class, (method, values) -> switch (method) {
            case "isConnected" -> connected.get();
            case "connect" -> { authArgs.set(values.length); connected.set(true); yield null; }
            case "getCaptchaImage" -> new BufferedImage(3, 2, BufferedImage.TYPE_INT_RGB);
            case "getAvailableInstruments" -> offered.keySet();
            case "getSubscribedInstruments" -> new HashSet<>(subscribed);
            case "unsubscribeInstruments" -> { subscribed.removeAll((Set<Instrument>) values[0]); yield null; }
            case "startStrategy" -> { strategy.set((IStrategy) values[0]); strategy.get().onStart(context); yield 7L; }
            case "disconnect" -> { connected.set(false); yield null; }
            default -> null;
        });
        try (Bridge bridge = new Bridge(() -> { factories.incrementAndGet(); return client; }, events::add)) {
            check(!bridge.handle(request("instruments")).get("ok").getAsBoolean(), "unauthenticated catalog must fail");
            JsonObject captcha = request("captcha"); captcha.addProperty("cacheDirectory", cache.toString());
            check(good(bridge, captcha).getAsJsonObject("result").get("imageDataUrl").getAsString().startsWith("data:image/png;base64,"), "PNG login challenge");
            JsonObject login = request("connect"); login.addProperty("username", "fake-user"); login.addProperty("password", "never-log-secret"); login.addProperty("pin", "fake-pin"); login.addProperty("environment", "live"); login.addProperty("cacheDirectory", cache.toString());
            JsonObject connectedInfo = good(bridge, login).getAsJsonObject("result");
            check(connectedInfo.get("instrumentCount").getAsInt() == offered.size(), "connection count includes every offered instrument");
            check(factories.get() == 1 && authArgs.get() == 4, "CAPTCHA and PIN login must share the same SDK client");
            JsonArray catalog = good(bridge, request("instruments")).getAsJsonArray("result");
            check(catalog.size() == offered.size(), "catalog includes all account-supported forex, metals, stocks, indices, commodities, ETFs and crypto");
            List<String> expectedNames = offered.keySet().stream().map(Instrument::name).sorted().toList();
            for (int i = 0; i < catalog.size(); i++) {
                JsonObject item = catalog.get(i).getAsJsonObject();
                String nativeName = expectedNames.get(i);
                check(item.get("nativeId").getAsString().equals(nativeName), "stable sorted native names");
                Instrument instrument = offered.keySet().stream().filter(value -> value.name().equals(nativeName)).findFirst().orElseThrow();
                check(item.get("market").getAsString().equals(offered.get(instrument)), "correct market for " + instrument);
                check(item.get("baseAsset").getAsString().equals(instrument.getPrimaryJFCurrency().getCurrencyCode()), "preserve non-ISO primary asset");
                check(item.get("quoteAsset").getAsString().equals(instrument.getSecondaryJFCurrency().getCurrencyCode()), "preserve quote asset");
                check(new java.math.BigDecimal(item.get("priceIncrement").getAsString()).scale() == instrument.getTickScale(), "SDK precision preserved");
                JsonObject metadata = item.getAsJsonObject("providerMetadata");
                check(metadata.get("sdkSymbol").getAsString().equals(instrument.toString()) && metadata.get("sdkType").getAsString().equals(instrument.getType().name()), "native symbol/type metadata preserved");
                check(metadata.get("volumeUnit").getAsString().equals(offered.get(instrument).equals("forex") ? "millions" : "provider units"), "non-FX volume has no invented currency unit");
                if (instrument == Instrument.BTCUSD) check(metadata.get("assetType").getAsString().equals("crypto"), "SDK FOREX crypto is discoverable as crypto CFD");
                if (instrument == offshoreYuan) check(metadata.get("assetType").getAsString().equals("forex"), "non-ISO fiat remains a forex asset");
            }
            JsonObject query = request("history"); query.add("feedKey", feed("EURUSD")); query.addProperty("limit", 3); query.addProperty("from", (now - 60000) / 1000); query.addProperty("to", now / 1000);
            JsonArray bars = good(bridge, query).getAsJsonArray("result"); check(bars.size() == 1, "history honors exclusive UTC seconds range");
            check(bars.get(0).getAsJsonObject().get("closed").getAsBoolean(), "past candles are closed");
            check(historyArguments.get()[2] == OfferSide.BID && historyArguments.get()[3] == Filter.WEEKENDS, "SDK native Bid candles and weekend filter");
            check(subscribed.isEmpty(), "one-shot history releases unused instruments");
            IBar originalActive = active.get();
            active.set(fake(IBar.class, (method, values) -> switch (method) {
                case "getTime" -> now; case "getOpen", "getHigh", "getLow", "getClose" -> 1.14; case "getVolume" -> 0.0; default -> null;
            }));
            JsonObject recent = query.deepCopy(); recent.addProperty("from", 0); recent.addProperty("to", 0);
            offline.set(true);
            check(good(bridge, recent).getAsJsonArray("result").size() == 2, "SDK current flat closure candle must not create a weekend bar");
            offline.set(false);
            check(good(bridge, recent).getAsJsonArray("result").size() == 3, "current flat opening candle remains during active market");
            active.set(originalActive);
            JsonObject subscription = request("subscribe"); subscription.addProperty("subscriptionId", "view-1"); subscription.add("feedKey", feed("EURUSD"));
            good(bridge, subscription); check(subscribed.contains(Instrument.EURUSD), "active chart owns SDK subscription");
            await(() -> { synchronized (events) { return events.stream().anyMatch(event -> event.has("bars")) && events.get(events.size() - 1).has("health") && events.get(events.size() - 1).getAsJsonObject("health").get("state").getAsString().equals("live"); } }, "asynchronous seed publishes bars and readiness");
            JsonObject liveBars = events.stream().filter(event -> event.has("bars")).reduce((first, second) -> second).orElseThrow();
            check(liveBars.getAsJsonArray("bars").size() == 3, "initial subscription publishes real SDK candles");
            JsonObject seededHealth = events.get(events.size() - 1).getAsJsonObject("health");
            check(!seededHealth.has("lastTradeAt"), "history must not invent a last quote or fetch a blocking tick cache");
            strategy.get().onTick(Instrument.EURUSD, tick(now + 1000, 1.14));
            JsonObject current = events.stream().filter(event -> event.has("bars")).reduce((first, second) -> second).orElseThrow().getAsJsonArray("bars").get(0).getAsJsonObject();
            check(!current.get("closed").getAsBoolean() && current.get("open").getAsDouble() == 1.10 && current.get("volume").getAsDouble() == 2.5, "tick uses SDK current OHLCV, preserving opening price and volume");
            Method heartbeat = Bridge.class.getDeclaredMethod("heartbeat"); heartbeat.setAccessible(true);
            heartbeat.invoke(bridge);
            JsonObject pulse = events.get(events.size() - 1).getAsJsonObject("health");
            check(pulse.get("lastTradeAt").getAsLong() == (now + 1000) / 1000, "heartbeat retains last actual quote time");
            offline.set(true); heartbeat.invoke(bridge);
            pulse = events.get(events.size() - 1).getAsJsonObject("health");
            check(pulse.get("state").getAsString().equals("marketClosed") && pulse.get("lastTradeAt").getAsLong() == (now + 1000) / 1000, "market closure preserves last actual quote time without inventing ticks");
            offline.set(false);
            int before = events.size(); strategy.get().onTick(Instrument.EURUSD, tick(now - 1, 1.13)); check(events.size() == before, "out-of-order ticks cannot rewind candles");
            strategy.get().onBar(Instrument.EURUSD, Period.ONE_MIN, null, bar(now - 60000, 1.08, 1.12, 1.07, 1.11));
            JsonObject closed = events.get(events.size() - 1).getAsJsonArray("bars").get(0).getAsJsonObject();
            check(closed.get("closed").getAsBoolean() && closed.get("quality").getAsString().equals("authoritative"), "onBar closes and reconciles exact Bid candle");
            JsonObject second = subscription.deepCopy(); second.addProperty("subscriptionId", "view-2"); good(bridge, second);
            JsonObject unsubscribe = request("unsubscribe"); unsubscribe.addProperty("subscriptionId", "view-1"); good(bridge, unsubscribe); check(subscribed.contains(Instrument.EURUSD), "shared instrument stays subscribed for remaining view");
            unsubscribe.addProperty("subscriptionId", "view-2"); good(bridge, unsubscribe); check(subscribed.isEmpty(), "last inactive chart unsubscribes SDK instrument");
            before = events.size(); strategy.get().onTick(Instrument.EURUSD, tick(now + 2000, 1.15)); check(events.size() == before, "inactive feeds emit no updates");
            for (var entry : offered.entrySet()) {
                Instrument instrument = entry.getKey();
                if (entry.getValue().equals("forex")) continue;
                JsonObject selectedFeed = feed(instrument, entry.getValue());
                JsonObject selectedHistory = query.deepCopy(); selectedHistory.add("feedKey", selectedFeed);
                check(good(bridge, selectedHistory).getAsJsonArray("result").size() == 1 && historyArguments.get()[0] == instrument, "non-FX history uses the exact SDK instrument");
                String subscriptionId = "asset-" + instrument.name();
                JsonObject selectedSubscription = request("subscribe"); selectedSubscription.addProperty("subscriptionId", subscriptionId); selectedSubscription.add("feedKey", selectedFeed);
                good(bridge, selectedSubscription);
                await(() -> { synchronized (events) { return events.stream().anyMatch(event -> event.get("subscriptionId").getAsString().equals(subscriptionId) && event.has("health") && event.getAsJsonObject("health").get("state").getAsString().equals("live")); } }, "non-FX seed finishes");
                check(subscribed.equals(Set.of(instrument)), "only selected non-FX instrument is subscribed");
                strategy.get().onTick(instrument, tick(now + 3000, 12.5));
                JsonObject latest = events.stream().filter(event -> event.has("bars")).reduce((first, secondEvent) -> secondEvent).orElseThrow();
                check(latest.get("subscriptionId").getAsString().equals(subscriptionId) && latest.getAsJsonArray("bars").get(0).getAsJsonObject().get("close").getAsDouble() == 12.5, "selected non-FX feed receives its real Bid ticks");
                unsubscribe.addProperty("subscriptionId", subscriptionId); good(bridge, unsubscribe);
                check(subscribed.isEmpty(), "non-FX unsubscribe releases SDK instrument");
                JsonObject wrongMarket = selectedHistory.deepCopy(); wrongMarket.getAsJsonObject("feedKey").addProperty("market", "forex");
                check(!bridge.handle(wrongMarket).get("ok").getAsBoolean(), "non-FX cannot masquerade as a fiat pair");
            }
            JsonObject unknown = query.deepCopy(); unknown.add("feedKey", feed("GBPUSD")); check(!bridge.handle(unknown).get("ok").getAsBoolean(), "unavailable account instrument fails closed");
            query.addProperty("limit", 5001); check(!bridge.handle(query).get("ok").getAsBoolean(), "bounded history requests");
            good(bridge, request("disconnect")); check(!connected.get(), "disconnect closes session");
        }
        try (Bridge bridge = new Bridge(() -> { throw new RuntimeException("fake-user never-log-secret fake-pin"); }, events::add)) {
            JsonObject login = request("connect"); login.addProperty("username", "fake-user"); login.addProperty("password", "never-log-secret"); login.addProperty("environment", "demo"); login.addProperty("cacheDirectory", cache.toString());
            String error = bridge.handle(login).toString(); check(!error.contains("never-log-secret") && !error.contains("fake-user"), "SDK failures must not disclose credentials");
        }
        AtomicBoolean retryConnected = new AtomicBoolean();
        AtomicInteger loginCalls = new AtomicInteger(), strategyCalls = new AtomicInteger();
        IClient startupFailure = fake(IClient.class, (method, values) -> switch (method) {
            case "isConnected" -> retryConnected.get();
            case "connect" -> { loginCalls.incrementAndGet(); retryConnected.set(true); yield null; }
            case "getAvailableInstruments" -> Set.of(Instrument.EURUSD);
            case "startStrategy" -> {
                if (strategyCalls.incrementAndGet() == 1) throw new IllegalStateException("explicit SDK strategy startup failure");
                ((IStrategy) values[0]).onStart(context); yield 9L;
            }
            case "disconnect" -> { retryConnected.set(false); yield null; }
            default -> null;
        });
        try (Bridge bridge = new Bridge(() -> startupFailure, events::add)) {
            JsonObject login = request("connect"); login.addProperty("username", "fake-user"); login.addProperty("password", "never-log-secret"); login.addProperty("environment", "demo"); login.addProperty("cacheDirectory", cache.toString());
            check(!bridge.handle(login).get("ok").getAsBoolean(), "initial strategy startup failure is reported");
            good(bridge, login);
            check(loginCalls.get() == 1 && strategyCalls.get() == 2, "retry restarts failed data strategy without reauthenticating the SDK session");
        }
        check(Bridge.isFiatForex(Instrument.EURUSD) && Bridge.isFiatForex(offshoreYuan)
            && !Bridge.isFiatForex(Instrument.BTCUSD), "fiat including non-ISO CNH stays separate from crypto");
        System.out.println("JForex bridge offline SDK protocol checks passed");
        Files.delete(cache);
    }
}
