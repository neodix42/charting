package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.google.gson.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;
import static cryptodesk.jforex.BridgeNativeDownloadTest.*;
import static cryptodesk.jforex.BridgeResponsivenessTest.*;

/** Independent SDK range loads must prove an empty getBars result, never invent prices. */
public final class BridgeHourlyRecoveryTest {
    static final long HOUR = 3_600_000, START = FROM * 1000;
    static final class Metadata {
        final AtomicReference<Long> first = new AtomicReference<>(START - HOUR);
        final AtomicInteger resets = new AtomicInteger(), bootstraps = new AtomicInteger();
        volatile Long bootstrapValue = START - HOUR;
        volatile Runnable bootstrapAction = () -> { };
        volatile com.dukascopy.charts.data.datacache.ICurvesProtocolHandler handler;
        com.dukascopy.charts.data.datacache.splits.ISplitsManager splits = fake(com.dukascopy.charts.data.datacache.splits.ISplitsManager.class,(method,args) -> null);
        com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager commissions = fake(com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager.class,(method,args) -> method.equals("applyFeedCommissionToData") ? args[3] : null);
        final com.dukascopy.charts.data.datacache.metadata.IFeedMetadataManager value = fake(
            com.dukascopy.charts.data.datacache.metadata.IFeedMetadataManager.class, (method, args) -> switch(method) {
                case "getTimeOfFirstCandleFast" -> first.get();
                case "resetTimeOfFirstData" -> { resets.incrementAndGet(); first.set(null); yield null; }
                case "getTimeOfFirstCandle" -> { bootstraps.incrementAndGet(); bootstrapAction.run(); first.set(bootstrapValue); yield bootstrapValue == null ? Long.MAX_VALUE : bootstrapValue; }
                default -> null;
            });
        IDataService service(List<org.threeten.extra.Interval> closed) {
            var schedule = new com.dukascopy.holiday.HolidayScheduleImpl.Builder(1).build();
            return calendarWithMetadata(true, () -> schedule, closed, value, handler, splits, commissions);
        }
    }
    static JsonObject query() {
        JsonObject query = history(true); query.getAsJsonObject("feedKey").addProperty("timeframe", "1h");
        query.addProperty("to", FROM + 3 *3600); query.addProperty("fullOpenHours", true); return query;
    }
    static Load emptyThenRecovery(NativeFixture f, CompletableFuture<JsonObject> reply) throws Exception {
        f.load().result.complete(List.of()); Load recovery = f.charts.load();
        check(!reply.isDone() && recovery.range() && recovery.args[3] == Filter.NO_FILTER
            && recovery.from() == START && recovery.end() == START +2*HOUR,
            "false-empty cache triggers exactly one independent bounded NO_FILTER UTC-day read");
        return recovery;
    }
    static void emit(Load load, long time, double open, double close, double low, double high, double volume) {
        ((LoadingDataListener) load.args[6]).newBar((Instrument) load.args[0], Period.ONE_HOUR, OfferSide.BID, time, open, close, low, high, volume);
    }
    static void run() throws Exception {
        for(boolean cancel:List.of(false,true)) {
            Metadata metadata=new Metadata();AtomicInteger attempts=new AtomicInteger();CountDownLatch nullReturned=new CountDownLatch(1);
            metadata.handler=fake(com.dukascopy.charts.data.datacache.ICurvesProtocolHandler.class,(method,args)->{
                if(!method.equals("loadChunkFileFromCFG"))return null;
                if(attempts.incrementAndGet()==1){nullReturned.countDown();return null;}
                return new com.dukascopy.charts.data.datacache.Data[]{new com.dukascopy.charts.data.datacache.CandleData(START,1,2,.5,3,1)};
            });
            try(NativeFixture f=new NativeFixture(50,6_500,metadata.service(List.of()))) {
                var reply=dispatch(f,query());f.load().result.complete(List.of());f.charts.load().finish(true);
                check(nullReturned.await(2,TimeUnit.SECONDS),"null CFG download reached bounded retry");
                if(cancel)f.bridge.close();
                JsonObject result=reply.get(8,TimeUnit.SECONDS);
                if(cancel){failed(result,"canceling the retry delay must not start another file download");check(attempts.get()==1,"cancellation interrupts the five-second wait");}
                else check(result.get("ok").getAsBoolean()&&result.getAsJsonArray("result").size()==1&&attempts.get()==2,"a transient null CFG download gets one same-file retry within the original work budget");
            }
        }
        {
            Metadata metadata=new Metadata(); List<String> adjustments=new ArrayList<>();
            metadata.handler=fake(com.dukascopy.charts.data.datacache.ICurvesProtocolHandler.class,(method,args) -> method.equals("loadChunkFileFromCFG") ? new com.dukascopy.charts.data.datacache.Data[]{
                new com.dukascopy.charts.data.datacache.CandleData(START,100,120,90,130,1),new com.dukascopy.charts.data.datacache.CandleData(START+24*HOUR,200,240,180,260,1)} : null);
            metadata.splits=fake(com.dukascopy.charts.data.datacache.splits.ISplitsManager.class,(method,args) -> {
                if (method.equals("applySplitsToData")) {
                    adjustments.add("split");
                    for (var item:(com.dukascopy.charts.data.datacache.Data[])args[1]) {
                        var candle=(com.dukascopy.charts.data.datacache.CandleData)item; candle.open/=2; candle.close/=2; candle.high/=2; candle.low/=2;
                    }
                } return null;
            });
            metadata.commissions=fake(com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager.class,(method,args) -> {
                if (!method.equals("applyFeedCommissionToData")) return null;
                check(args[0]==Instrument.BTCUSD && args[1]==Period.ONE_HOUR && args[2]==OfferSide.BID,"raw recovery uses the SDK's native BID commission convention");
                adjustments.add("commission");
                var data=(com.dukascopy.charts.data.datacache.Data[])args[3];
                for(var item:data) {
                    var candle=(com.dukascopy.charts.data.datacache.CandleData)item;
                    if(!candle.feedCommissionApplied) { candle.open+=10;candle.close+=10;candle.high+=10;candle.low+=10;candle.feedCommissionApplied=true; }
                }return data;
            });
            try(NativeFixture f=new NativeFixture(50,1_000,metadata.service(List.of()))) {
                JsonObject query=query();query.addProperty("to",FROM+2*86400);var reply=dispatch(f,query);
                f.load().result.complete(List.of());f.charts.load().finish(true);f.load().result.complete(List.of());
                JsonObject result=reply.get(2,TimeUnit.SECONDS);
                check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").get(0).getAsJsonObject().get("open").getAsDouble()==60
                    && result.getAsJsonArray("result").get(1).getAsJsonObject().get("open").getAsDouble()==110 && adjustments.equals(List.of("split","commission")),
                    "raw monthly files mirror normal SDK split-then-commission pricing exactly once before immutable reuse");
            }
        }
        for (String kind : List.of("observed", "gap", "null", "empty", "filled", "duplicate", "outside", "price", "type", "oversized", "failure", "progress-failed", "default-change", "metadata-change", "adjustment-time", "timeout")) {
            Metadata metadata = new Metadata(); AtomicInteger files = new AtomicInteger();
            long past = java.time.Instant.parse("2024-01-01T00:00:00Z").toEpochMilli(); metadata.first.set(past-HOUR);
            if(kind.equals("adjustment-time"))metadata.commissions=fake(com.dukascopy.charts.data.datacache.feed.IFeedCommissionManager.class,(method,args)->{
                if(!method.equals("applyFeedCommissionToData"))return null;
                var rows=(com.dukascopy.charts.data.datacache.Data[])args[3];for(var row:rows)row.time+=HOUR;return rows;
            });
            AtomicReference<NativeFixture> owner = new AtomicReference<>();
            metadata.handler = fake(com.dukascopy.charts.data.datacache.ICurvesProtocolHandler.class, (method,args) -> {
                if (!method.equals("loadChunkFileFromCFG")) return null;
                files.incrementAndGet();
                check(args[0] == Instrument.BTCUSD && args[1] == Period.ONE_HOUR && args[2] == OfferSide.BID && (Long) args[3] == past && Boolean.FALSE.equals(args[4]), "direct recovery requests the aligned normal monthly BID file, never decodes month offsets from a day timestamp");
                if (kind.equals("null")) return null;
                if (kind.equals("failure")) throw new com.dukascopy.charts.data.datacache.DataCacheException("explicit file failure");
                if (kind.equals("empty")) return new com.dukascopy.charts.data.datacache.Data[0];
                if (kind.equals("type")) return new com.dukascopy.charts.data.datacache.Data[]{new com.dukascopy.charts.data.datacache.TickData()};
                if (kind.equals("oversized")) return new com.dukascopy.charts.data.datacache.Data[745];
                if (kind.equals("default-change")) owner.get().charts.defaultFeed.set(null);
                if (kind.equals("metadata-change")) metadata.first.set(past-2*HOUR);
                if (kind.equals("progress-failed")) ((com.dukascopy.charts.data.datacache.ILoadingProgressListener) args[5]).loadingFinished(false,past,past,past,null);
                if (kind.equals("timeout")) { try { Thread.sleep(5_000); } catch (InterruptedException ignored) { } }
                long stamp = kind.equals("gap") ? past+3*24*HOUR : kind.equals("outside") ? past+32*24*HOUR : past+24*HOUR;
                var candle = new com.dukascopy.charts.data.datacache.CandleData(stamp, kind.equals("price") ? -1 : 1,2,.5,3,1);
                if (kind.equals("filled")) candle = new com.dukascopy.charts.data.datacache.CandleData(stamp,2,2,2,2,0);
                if (kind.equals("duplicate")) return new com.dukascopy.charts.data.datacache.Data[]{candle,candle};
                return new com.dukascopy.charts.data.datacache.Data[]{candle};
            });
            try (NativeFixture f = new NativeFixture(50, kind.equals("timeout") ? 150 : 1_000, metadata.service(List.of()),java.time.Instant.parse("2024-03-01T00:00:00Z").toEpochMilli())) {
                owner.set(f); JsonObject query=query(); query.addProperty("from", past/1000+86400); query.addProperty("to", past/1000+86400+3*3600);
                var reply = dispatch(f,query); f.load().result.complete(List.of()); Load recovery=f.charts.load(); recovery.finish(true);
                JsonObject result=reply.get(2, TimeUnit.SECONDS);
                if (kind.equals("observed") || kind.equals("gap")) check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() == (kind.equals("observed") ? 1 : 0), "actual nonempty historical month recovers observations or proves a member-day gap "+kind);
                else failed(result,"missing, empty, invalid, failed or changed monthly file is never accepted as zero history "+kind);
                if(kind.equals("null")) check(result.getAsJsonObject("error").get("kind").getAsString().equals("timeout"),"insufficient original budget for a null-file retry retains the typed hourly timeout");
                check(files.get() ==1, "one bounded direct file attempt follows an ambiguous cached empty response");
            }
        }
        {
            Metadata metadata = new Metadata(); AtomicInteger files = new AtomicInteger();
            metadata.handler = fake(com.dukascopy.charts.data.datacache.ICurvesProtocolHandler.class,(method,args) -> {
                if (!method.equals("loadChunkFileFromCFG")) return null; files.incrementAndGet();
                return new com.dukascopy.charts.data.datacache.Data[]{new com.dukascopy.charts.data.datacache.CandleData(START,1,2,.5,3,1),new com.dukascopy.charts.data.datacache.CandleData(START+24*HOUR,2,3,1,4,0)};
            });
            try (NativeFixture f = new NativeFixture(50,1_000,metadata.service(List.of()))) {
                JsonObject query=query(); query.addProperty("to",FROM+2*86400); var reply=dispatch(f,query);
                f.load().result.complete(List.of()); f.charts.load().finish(true); f.load().result.complete(List.of());
                JsonObject result=reply.get(2,TimeUnit.SECONDS);
                check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size()==2 && files.get()==1 && f.charts.loads.isEmpty(), "verified monthly response is reused across false-empty source days within one bounded export");
            }
        }
        for (String kind : List.of("known", "null-reload", "max-reload")) {
            Metadata metadata = new Metadata();
            if (!kind.equals("known")) metadata.first.set(kind.equals("max-reload") ? Long.MAX_VALUE : null);
            var closed = List.of(org.threeten.extra.Interval.of(java.time.Instant.ofEpochMilli(START+HOUR+1), java.time.Instant.ofEpochMilli(START+HOUR+100)));
            try (NativeFixture f = new NativeFixture(50, 1_000, metadata.service(closed))) {
                var reply = dispatch(f, query()); Load recovery = emptyThenRecovery(f, reply);
                recovery.emit(START); recovery.emit(START+HOUR); emit(recovery, START+2*HOUR, 2,3,1,4,0); recovery.finish(true);
                JsonObject result = reply.get(2, TimeUnit.SECONDS);
                check(result.get("ok").getAsBoolean() && result.getAsJsonArray("result").size() ==2,
                    "independent genuine observations recover false emptiness, retain changing zero-volume OHLC, and apply historical closures " +kind);
                check(metadata.resets.get() == (kind.equals("known") ? 0 : 1) && metadata.bootstraps.get() == metadata.resets.get(), "only unknown/MAX metadata gets one RAM reload");
                recovery.emit(START+10*HOUR); recovery.finish(false);
                check(result.getAsJsonArray("result").size() ==2, "stale callbacks after successful completion cannot change published observations");
            }
        }
        for (String kind : List.of("known-empty", "placeholder-only", "before-availability")) {
            Metadata metadata = new Metadata(); if (kind.equals("before-availability")) metadata.first.set(START+24*HOUR);
            try (NativeFixture f = new NativeFixture(50, 1_000, metadata.service(List.of()))) {
                var reply = dispatch(f, query()); Load recovery = emptyThenRecovery(f, reply);
                if (kind.equals("placeholder-only")) emit(recovery, START,2,2,2,2,0);
                recovery.finish(true); JsonObject result = reply.get(2, TimeUnit.SECONDS);
                failed(result, "SDK complete-empty is not sufficient evidence of absent observations, even with finite metadata " +kind);
            }
        }
        for (String kind : List.of("null", "max", "zero", "future", "offline-bootstrap")) {
            Metadata metadata = new Metadata(); metadata.first.set(switch(kind) { case "null" -> null; case "zero" -> 0L; case "future" -> (TO+86400)*1000; default -> Long.MAX_VALUE; });
            metadata.bootstrapValue = kind.equals("null") ? null : kind.equals("offline-bootstrap") ? START-HOUR : Long.MAX_VALUE;
            try (NativeFixture f = new NativeFixture(50, 1_000, metadata.service(List.of()))) {
                if (kind.equals("offline-bootstrap")) metadata.bootstrapAction = () -> f.charts.connected.set(false);
                var reply = dispatch(f, query()); f.load().result.complete(List.of()); failed(reply.get(2, TimeUnit.SECONDS), "unknown or offline metadata cannot certify an empty historical day " +kind);
                check(f.charts.loads.isEmpty() && metadata.resets.get() == (Set.of("null","max","offline-bootstrap").contains(kind) ? 1 : 0), "unknown metadata bounded reload never treats MAX/offline initialization as absence");
            }
        }
        for (String kind : List.of("partial", "wrong-bounds", "duplicate", "outside", "price", "unaligned", "wrong-source", "before-first", "metadata-change", "disconnect", "account-change", "context-change")) {
            Metadata metadata = new Metadata(); if (kind.equals("before-first")) metadata.first.set(START+HOUR);
            try (NativeFixture f = new NativeFixture(50, 1_000, metadata.service(List.of()))) {
                var reply = dispatch(f, query()); Load recovery = emptyThenRecovery(f, reply);
                switch(kind) {
                    case "partial" -> recovery.finish(false);
                    case "wrong-bounds" -> recovery.progress().loadingFinished(true, START+HOUR, recovery.end(), recovery.end());
                    case "duplicate" -> { recovery.emit(START); recovery.emit(START); }
                    case "outside" -> recovery.emit(START+3*HOUR);
                    case "price" -> emit(recovery, START,-1,2,.5,3,1);
                    case "unaligned" -> recovery.emit(START+1);
                    case "wrong-source" -> ((LoadingDataListener) recovery.args[6]).newBar(Instrument.EURUSD, Period.ONE_HOUR, OfferSide.BID, START,1,2,.5,3,1);
                    case "before-first" -> recovery.emit(START);
                    case "metadata-change" -> metadata.first.set(START-2*HOUR);
                    case "disconnect" -> f.charts.connected.set(false);
                    case "account-change" -> f.charts.accountId.set("different-offline-account");
                    case "context-change" -> f.charts.strategy.get().onStop();
                }
                recovery.finish(true); failed(reply.get(2, TimeUnit.SECONDS), "incomplete, invalid or changed-source recovery must not publish/cache " +kind);
                await(() -> recovery.progress().stopJob(), "failed independent loader is stopped"); recovery.emit(START); recovery.finish(true);
            }
        }
        Metadata metadata = new Metadata();
        try (NativeFixture f = new NativeFixture(50, 150, metadata.service(List.of()))) {
            var reply = dispatch(f, query()); Load recovery = emptyThenRecovery(f, reply);
            JsonObject result = reply.get(2, TimeUnit.SECONDS); failed(result, "independent historical recovery shares the native job deadline");
            check(result.getAsJsonObject("error").get("kind").getAsString().equals("timeout"), "stalled recovery remains an explicit timeout");
            await(() -> recovery.progress().stopJob(), "timeout interrupts and stops the independent loader");
            recovery.emit(START); recovery.finish(true);
            var retry = dispatch(f, query()); f.load().result.complete(List.of(bar(START,1,3,.5,2)));
            check(retry.get(2, TimeUnit.SECONDS).getAsJsonArray("result").size() ==1, "late canceled callbacks cannot poison the next valid export");
        }
        metadata = new Metadata(); metadata.first.set(Long.MAX_VALUE);
        try (NativeFixture f = new NativeFixture(50, 150, metadata.service(List.of()))) {
            AtomicBoolean interrupted = new AtomicBoolean(); CountDownLatch finishedMetadata = new CountDownLatch(1);
            metadata.bootstrapAction = () -> { try { Thread.sleep(2_000); } catch (InterruptedException error) { interrupted.set(true); } finally { finishedMetadata.countDown(); } };
            var reply = dispatch(f, query()); f.load().result.complete(List.of());
            failed(reply.get(2, TimeUnit.SECONDS), "metadata getter consuming an interrupt cannot extend the native work deadline");
            check(finishedMetadata.await(2, TimeUnit.SECONDS) && interrupted.get(), "outer timeout interrupts metadata loading");
            await(f.charts.subscribed::isEmpty, "expired metadata recovery releases its source without starting a later range load");
            check(f.charts.loads.isEmpty(), "metadata returning after its deadline cannot launch an unbounded follow-up SDK load");
        }
        try (NativeFixture f = new NativeFixture(50, 150, new Metadata().service(List.of()))) {
            JsonObject query = query(); query.addProperty("to", FROM+2*86400);
            var reply = dispatch(f, query); NativeLoad blocked = f.load(); blocked.returnAfterInterrupt.set(true);
            failed(reply.get(2, TimeUnit.SECONDS), "SDK collection consuming an interrupt cannot continue into another source day");
            await(f.charts.subscribed::isEmpty, "expired collection releases its source after the swallowed interrupt");
            check(blocked.canceled.get() && f.calls.isEmpty() && f.charts.loads.isEmpty(), "absolute native budget also stops the daily getBars loop");
        }
        metadata = new Metadata(); metadata.first.set(Long.MAX_VALUE);
        try (NativeFixture f = new NativeFixture(50, 1_000, metadata.service(List.of()))) {
            JsonObject query = query(); query.addProperty("to", FROM+2*86400);
            var reply = dispatch(f, query); f.load().result.complete(List.of()); Load first = f.charts.load(); first.emit(START); first.finish(true);
            NativeLoad second = f.load(); metadata.first.set(Long.MAX_VALUE); second.result.complete(List.of());
            failed(reply.get(2, TimeUnit.SECONDS), "one export cannot repeatedly reload invalid metadata across empty days");
            check(metadata.resets.get() ==1 && metadata.bootstraps.get() ==1 && f.charts.loads.isEmpty(), "unknown metadata retry remains bounded across the complete export");
        }
        System.out.println("JForex false-empty hourly cache recovery, loaded metadata, exact completion, genuine gaps, source fencing and cancellation checks passed");
    }
}
