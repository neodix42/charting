package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.api.impl.connect.PlatformOrderImpl;
import com.dukascopy.api.system.IClient;
import com.google.gson.JsonObject;
import java.lang.reflect.Field;
import java.nio.file.Files;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;

/** Partial fills retain exact native identity even when the SDK loses originalAmount. */
public final class BridgePartialFillIdentityTest {
    private static final String INTENT = "abcdef0123456789abcdef0123456789";
    private static void field(PlatformOrderImpl order, String name, Object value) throws Exception {
        Field field = PlatformOrderImpl.class.getDeclaredField(name); field.setAccessible(true); field.set(order, value);
    }
    @SuppressWarnings("unchecked")
    private static void pinnedSdkCopyDropsOriginalAmount() throws Exception {
        // These are local SDK objects with no task manager, account or transport.
        // Never call a setter that submits an SDK request or any order action.
        PlatformOrderImpl original = new PlatformOrderImpl(null);
        field(original, "groupId", "offline-partial-id"); field(original, "label", "CD_" + INTENT);
        field(original, "instrument", Instrument.AAPLUSUSD); field(original, "orderCommand", IEngine.OrderCommand.BUY);
        field(original, "state", IOrder.State.FILLED); field(original, "requestedAmount", .0005);
        field(original, "filledAmount", .000026); field(original, "filledAmountInitial", .000026);
        original.setOriginalAmount(.0005);
        Field fills = PlatformOrderImpl.class.getDeclaredField("fillHistory"); fills.setAccessible(true);
        ((List<IFillOrder>) fills.get(original)).add(fake(IFillOrder.class, (name, args) -> name.equals("getAmount") ? .000026 : null));
        PlatformOrderImpl copy = new PlatformOrderImpl(null, original);
        check(original.getOriginalAmount() == .0005 && copy.getOriginalAmount() == 0, "pinned SDK copy loses original amount rather than changing the actual partial fill");
        check(copy.getRequestedAmount() == .0005 && copy.getAmount() == .000026 && copy.getFillHistory().get(0).getAmount() == .000026,
            "SDK copy retains requested amount and the actual 26-of-500 fill");
        check(copy.getId().equals(original.getId()) && copy.getLabel().equals(original.getLabel()) && copy.getInstrument() == original.getInstrument() && copy.isLong() == original.isLong(),
            "SDK copy retains exact platform native identity");
    }
    static void run() throws Exception {
        pinnedSdkCopyDropsOriginalAmount();
        try (Fixture fixture = new Fixture()) {
            JsonObject lookup = fixture.lookup();
            check(good(fixture.bridge, lookup).getAsJsonObject("result").get("originalQuantity").getAsDouble() == 500,
                "normal original quantity keeps the existing exact-request ownership path");
            fixture.original.set(0.0);
            rejected(fixture, lookup, "missing original cannot attribute itself without a durable fill witness");
            JsonObject tiny = lookup.deepCopy(); tiny.addProperty("quantity", 1e-8);
            rejected(fixture, tiny, "absolute quantity tolerance cannot turn a missing original into a tiny requested amount");
            lookup.addProperty("knownFilledQuantity", 26);
            JsonObject found = good(fixture.bridge, lookup).getAsJsonObject("result");
            check(found.get("originalQuantity").getAsDouble() == 0 && found.get("filledQuantity").getAsDouble() == 26 && found.get("remainingQuantity").getAsDouble() == 26,
                "verified 26-of-500 fill remains open without inventing original or remaining quantity");
            check(found.get("stopLoss").getAsDouble() == 99 && found.get("takeProfit").getAsDouble() == 102,
                "identity recovery reports the broker protective prices without modifying them");
            fixture.splitFills.set(true); fixture.remaining.set(20.0);
            JsonObject partlyClosed = good(fixture.bridge, lookup).getAsJsonObject("result");
            check(partlyClosed.get("filledQuantity").getAsDouble() == 26 && partlyClosed.get("remainingQuantity").getAsDouble() == 20,
                "all positive native fills are summed while a partial close keeps only the remaining exposure");
            fixture.splitFills.set(false); fixture.remaining.set(26.0);
            fixture.original.set(26.0);
            check(good(fixture.bridge, lookup).getAsJsonObject("result").get("originalQuantity").getAsDouble() == 26,
                "a reconstructed original equal to the exact durable fill can be reconciled");
            fixture.original.set(0.0);
            for (double witness : new double[]{0, -1, 25, 27, 501, Double.NaN, Double.POSITIVE_INFINITY}) {
                JsonObject invalid = lookup.deepCopy(); invalid.addProperty("knownFilledQuantity", witness);
                rejected(fixture, invalid, "invalid or different durable fill must not match");
            }
            for (double original : new double[]{-1, 25, 27, Double.NaN, Double.POSITIVE_INFINITY}) {
                fixture.original.set(original); rejected(fixture, lookup, "an unrelated or invalid original quantity must not match");
            }
            fixture.original.set(0.0); fixture.fillsPresent.set(false); rejected(fixture, lookup, "missing fill history cannot prove a recovered order");
            fixture.fillsPresent.set(true);
            for (double filled : new double[]{0, -1, 25, 27, Double.NaN, Double.POSITIVE_INFINITY}) {
                fixture.filled.set(filled); rejected(fixture, lookup, "every source fill must be finite, positive and cover the witnessed actual fill");
            }
            fixture.filled.set(26.0);
            for (double remaining : new double[]{-1, 27, Double.NaN, Double.POSITIVE_INFINITY}) {
                fixture.remaining.set(remaining); rejected(fixture, lookup, "remaining exposure must be finite and within the confirmed filled quantity");
            }
            fixture.remaining.set(26.0); fixture.foreignLabel.set(true); rejected(fixture, lookup, "a foreign label cannot use the saved fill witness"); fixture.foreignLabel.set(false);
            fixture.foreignID.set(true); rejected(fixture, lookup, "a foreign native order ID cannot use the saved fill witness"); fixture.foreignID.set(false);
            fixture.foreignInstrument.set(true); rejected(fixture, lookup, "a different instrument cannot use the saved fill witness"); fixture.foreignInstrument.set(false);
            fixture.longSide.set(false); rejected(fixture, lookup, "a different position side cannot use the saved fill witness"); fixture.longSide.set(true);
            JsonObject noID = lookup.deepCopy(); noID.remove("orderID"); rejected(fixture, noID, "label-only legacy lookup cannot use missing-original recovery");
            // Guard again on the SDK strategy thread, after the initial ownership read.
            fixture.beforeExecution.set(() -> fixture.original.set(27.0));
            rejected(fixture, fixture.closeRequest(lookup), "a changed original must not be closed"); fixture.original.set(0.0);
            fixture.beforeExecution.set(() -> fixture.filled.set(25.0));
            rejected(fixture, fixture.closeRequest(lookup), "changed filled evidence must not be closed"); fixture.filled.set(26.0);
            fixture.beforeExecution.set(() -> fixture.remaining.set(25.0));
            rejected(fixture, fixture.closeRequest(lookup), "a changed remaining amount must not be closed"); fixture.remaining.set(26.0);
            fixture.beforeExecution.set(() -> fixture.foreignID.set(true));
            rejected(fixture, fixture.closeRequest(lookup), "a changed native ID must not be closed"); fixture.foreignID.set(false);
            fixture.beforeExecution.set(() -> fixture.foreignInstrument.set(true));
            rejected(fixture, fixture.closeRequest(lookup), "a changed instrument on the same SDK object must not be closed"); fixture.foreignInstrument.set(false);
            fixture.beforeExecution.set(() -> fixture.longSide.set(false));
            rejected(fixture, fixture.closeRequest(lookup), "a changed position side on the same SDK object must not be closed"); fixture.longSide.set(true);
            fixture.original.set(500.0);
            JsonObject tinyClose = fixture.closeRequest(lookup); tinyClose.addProperty("remainingQuantity", 1e-8);
            fixture.beforeExecution.set(() -> fixture.remaining.set(0.0));
            rejected(fixture, tinyClose, "absolute quantity tolerance cannot turn zero current exposure into a closeable tiny amount");
            fixture.original.set(0.0); fixture.remaining.set(26.0);
            check(fixture.closes.get() == 0, "failed identity or size guards dispatch no close action");
            JsonObject close = fixture.closeRequest(lookup);
            JsonObject closed = good(fixture.bridge, close).getAsJsonObject("result");
            check(closed.get("status").getAsString().equals("closed") && closed.get("remainingQuantity").getAsDouble() == 0 && fixture.closes.get() == 1,
                "only the exact confirmed remaining native position is closed on the strategy thread");
            rejected(fixture, close, "a close confirmation remains single-use after recovery");
            JsonObject historical = good(fixture.bridge, lookup).getAsJsonObject("result");
            check(historical.get("status").getAsString().equals("closed") && historical.get("originalQuantity").getAsDouble() == 0 && historical.get("filledQuantity").getAsDouble() == 26,
                "recovered closed SDK history replaces a stale local open status without inventing its original size");
            check(fixture.opens.get() == 0 && fixture.protectionChanges.get() == 0, "reconciliation creates no new entries or protective-level changes");
        }
        System.out.println("JForex partial-fill SDK copy, exact witnessed identity and guarded close checks passed");
    }
    private static void rejected(Fixture fixture, JsonObject request, String message) {
        check(!fixture.bridge.handle(request).get("ok").getAsBoolean(), message);
    }
    private static final class Fixture implements AutoCloseable {
        final AtomicReference<Double> original = new AtomicReference<>(500.0), filled = new AtomicReference<>(26.0), remaining = new AtomicReference<>(26.0);
        final AtomicBoolean connected = new AtomicBoolean(), strategyThread = new AtomicBoolean(), fillsPresent = new AtomicBoolean(true), splitFills = new AtomicBoolean(), foreignLabel = new AtomicBoolean(), foreignID = new AtomicBoolean(), foreignInstrument = new AtomicBoolean(), longSide = new AtomicBoolean(true);
        final AtomicReference<IOrder.State> state = new AtomicReference<>(IOrder.State.FILLED);
        final AtomicReference<Runnable> beforeExecution = new AtomicReference<>();
        final AtomicInteger closes = new AtomicInteger(), opens = new AtomicInteger(), protectionChanges = new AtomicInteger(), closeIDs = new AtomicInteger();
        final long openedAt = System.currentTimeMillis() - 1000;
        final Bridge bridge;
        Fixture() throws Exception {
            IFillOrder fill = fake(IFillOrder.class, (name, args) -> name.equals("getAmount") ? filled.get() / 1_000_000.0 : null);
            IFillOrder halfFill = fake(IFillOrder.class, (name, args) -> name.equals("getAmount") ? filled.get() / 2_000_000.0 : null);
            IOrder order = fake(IOrder.class, (name, args) -> switch (name) {
                case "getId" -> foreignID.get() ? "foreign-id" : "offline-partial-id"; case "getLabel" -> foreignLabel.get() ? "external-position" : "CD_" + INTENT;
                case "getInstrument" -> foreignInstrument.get() ? Instrument.EURUSD : Instrument.AAPLUSUSD; case "isLong" -> longSide.get();
                case "getOriginalAmount" -> original.get() / 1_000_000.0; case "getRequestedAmount" -> .0005;
                case "getAmount" -> remaining.get() / 1_000_000.0; case "getState" -> state.get(); case "getFillTime" -> openedAt;
                case "getCloseTime" -> state.get() == IOrder.State.CLOSED ? openedAt + 500 : 0L; case "getOpenPrice" -> 100.3;
                case "getStopLossPrice" -> 99.0; case "getTakeProfitPrice" -> 102.0;
                case "getFillHistory" -> !fillsPresent.get() ? List.of() : splitFills.get() ? List.of(halfFill, halfFill) : List.of(fill); case "getCloseHistory" -> List.of();
                case "close" -> { check(strategyThread.get(), "only the SDK strategy thread may close"); closes.incrementAndGet(); state.set(IOrder.State.CLOSED); yield null; }
                case "setStopLossPrice", "setTakeProfitPrice" -> { protectionChanges.incrementAndGet(); throw new AssertionError("identity reconciliation must not change protection"); }
                default -> null;
            });
            IEngine engine = fake(IEngine.class, (name, args) -> switch (name) {
                case "getOrder", "getOrderById" -> state.get() == IOrder.State.FILLED ? order : null;
                case "submitOrder" -> { opens.incrementAndGet(); throw new AssertionError("history and close must not open a position"); }
                default -> null;
            });
            IHistory history = fake(IHistory.class, (name, args) -> switch (name) { case "getHistoricalOrderById" -> order; case "getOrdersHistory" -> List.of(order); default -> null; });
            IAccount account = fake(IAccount.class, (name, args) -> name.equals("getAccountCurrency") ? JFCurrency.getInstance("HKD") : null);
            IContext context = fake(IContext.class, (name, args) -> switch (name) {
                case "getEngine" -> engine; case "getHistory" -> history; case "getAccount" -> account;
                case "executeTask" -> { Runnable mutation = beforeExecution.getAndSet(null); if (mutation != null) mutation.run(); FutureTask<Object> task = new FutureTask<>((Callable<Object>) args[0]); strategyThread.set(true); try { task.run(); } finally { strategyThread.set(false); } yield task; }
                default -> null;
            });
            IClient client = fake(IClient.class, (name, args) -> switch (name) {
                case "isConnected" -> connected.get(); case "connect" -> { connected.set(true); yield null; }
                case "getAvailableInstruments" -> Set.of(Instrument.AAPLUSUSD, Instrument.EURUSD); case "startStrategy" -> { ((IStrategy) args[0]).onStart(context); yield 1L; }
                default -> null;
            });
            bridge = new Bridge(() -> client, event -> {}, 100);
            JsonObject login = request("connect"); login.addProperty("username", "offline-partial-user"); login.addProperty("password", "offline-partial-password"); login.addProperty("environment", "demo");
            login.addProperty("cacheDirectory", Files.createTempDirectory("jforex-partial-offline-").toString()); good(bridge, login);
        }
        JsonObject lookup() { JsonObject request = BridgeTradingTest.trade("tradeHistory", Instrument.AAPLUSUSD, "buy", 500, 99, 102); request.addProperty("orderID", "offline-partial-id"); request.addProperty("from", openedAt - 1000); request.addProperty("includeValuation", false); return request; }
        JsonObject closeRequest(JsonObject lookup) { JsonObject request = lookup.deepCopy(); request.addProperty("op", "tradeClose"); request.addProperty("closeIntent", String.format("%032x", closeIDs.incrementAndGet())); request.addProperty("remainingQuantity", 26); return request; }
        public void close() { bridge.close(); }
    }
}
