package cryptodesk.jforex;

import com.dukascopy.api.*;
import com.dukascopy.charts.data.datacache.*;
import com.google.gson.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import static cryptodesk.jforex.BridgeTest.*;
import static cryptodesk.jforex.BridgeNativeDownloadTest.*;
import static cryptodesk.jforex.BridgeResponsivenessTest.*;

/** Canonical complete-past-year recovery selects only BID without inventing gaps. */
public final class BridgeYearlyRecoveryTest {
    static final long HOUR=3_600_000,DAY=24*HOUR,NOW=Instant.parse("2026-11-25T12:00:00Z").toEpochMilli();
    static Data[] year(long start,long finish,long observed) {
        var rows=new Data[(int)((finish-start)/HOUR*2)];
        for(int index=0;index<rows.length;index+=2){
            long time=start+(index/2)*HOUR;boolean active=time==observed;
            rows[index]=new CandleData(time,200,active?203:200,active?199:200,active?205:200,active?1:0);
            rows[index+1]=new CandleData(time,100,active?103:100,active?99:100,active?105:100,active?1:0);
        }return rows;
    }
    static JsonObject query(Instrument instrument,long from,long to){
        JsonObject query=history(instrument,Bridge.market(instrument),true);query.getAsJsonObject("feedKey").addProperty("timeframe","1h");
        query.addProperty("from",from/1000);query.addProperty("to",to/1000);query.addProperty("fullOpenHours",true);return query;
    }
    static void run()throws Exception{
        for(String label:List.of("gold","index","gap","truncated","odd","pair-time","duplicate","unaligned","price","ask-price","type","filled","source","metadata","default","progress","timeout","month-null","month-empty","month-null-failed","month-empty-failed","month-late-failed")){
            boolean index=label.equals("index");Instrument instrument=index?Instrument.USA500IDXUSD:Instrument.XAUUSD;
            long target=Instant.parse(index?"2024-12-01T00:00:00Z":"2025-11-02T00:00:00Z").toEpochMilli();
            long start=Instant.parse(index?"2024-01-01T00:00:00Z":"2025-01-01T00:00:00Z").toEpochMilli();
            long finish=Instant.parse(index?"2025-01-01T00:00:00Z":"2026-01-01T00:00:00Z").toEpochMilli();
            var metadata=new BridgeHourlyRecoveryTest.Metadata();metadata.first.set(Instant.parse("2010-01-01T00:00:00Z").toEpochMilli());
            Data[] payload=year(start,finish,label.equals("gap")?start:label.equals("filled")?-1:target+23*HOUR);
            int position=(int)((target+23*HOUR-start)/HOUR*2);AtomicReference<NativeFixture> owner=new AtomicReference<>();List<Boolean> routes=new ArrayList<>();AtomicReference<ILoadingProgressListener> oldProgress=new AtomicReference<>();
            metadata.handler=fake(ICurvesProtocolHandler.class,(method,args)->{
                if(!method.equals("loadChunkFileFromCFG"))return null;
                boolean large=(Boolean)args[4];routes.add(large);
                check(args[0]==instrument&&args[1]==Period.ONE_HOUR&&args[2]==OfferSide.BID,"same SDK instrument and BID side are retained across archive routes");
                if(!large){
                    check(label.startsWith("month-"),"invalid or changed positive year data never authorizes monthly fallback");
                    if(label.equals("month-late-failed"))oldProgress.get().loadingFinished(false,start,finish,start,null);
                    return new Data[]{new CandleData(target+23*HOUR,100,103,99,105,1)};
                }
                check((Long)args[3]==start,"closed past-year recovery starts at exact UTC January1 before implicit timestamps decode");
                if(label.startsWith("month-")){
                    oldProgress.set((ILoadingProgressListener)args[5]);
                    if(label.endsWith("-failed")&&!label.equals("month-late-failed"))oldProgress.get().loadingFinished(false,start,finish,start,null);
                    return label.startsWith("month-empty")?new Data[0]:null;
                }
                if(label.equals("source"))owner.get().charts.accountId.set("changed-source");
                if(label.equals("metadata"))metadata.first.set(start-HOUR);
                if(label.equals("default"))owner.get().charts.defaultFeed.set(null);
                if(label.equals("progress"))((ILoadingProgressListener)args[5]).loadingFinished(false,start,finish,start,null);
                if(label.equals("timeout")){try{Thread.sleep(5_000);}catch(InterruptedException ignored){}}
                if(label.equals("truncated"))return Arrays.copyOf(payload,payload.length-2);
                if(label.equals("odd"))return Arrays.copyOf(payload,payload.length-1);
                if(label.equals("pair-time"))payload[position+1].time+=HOUR;
                if(label.equals("duplicate")){payload[position].time-=HOUR;payload[position+1].time-=HOUR;}
                if(label.equals("unaligned")){payload[position].time++;payload[position+1].time++;}
                if(label.equals("price"))((CandleData)payload[position+1]).close=Double.NaN;
                if(label.equals("ask-price"))((CandleData)payload[position]).low=-1;
                if(label.equals("type"))payload[position+1]=new TickData();
                return payload;
            });
            try(NativeFixture f=new NativeFixture(50,label.equals("timeout")?150:2_000,metadata.service(List.of()),NOW)){
                owner.set(f);var reply=dispatch(f,query(instrument,target,target+DAY));f.load().result.complete(List.of());f.charts.load().finish(true);
                JsonObject result=reply.get(3,TimeUnit.SECONDS);
                boolean success=Set.of("gold","index","gap").contains(label)||label.startsWith("month-");
                if(success){
                    check(result.get("ok").getAsBoolean(),"canonical year recovery succeeds for "+label+": "+result);
                    var bars=result.getAsJsonArray("result");check(bars.size()==(label.equals("gap")?0:1),"only actual observed BID rows survive, without flat carries");
                    if(!bars.isEmpty())check(bars.get(0).getAsJsonObject().get("open").getAsDouble()==100&&bars.get(0).getAsJsonObject().get("time").getAsLong()==(target+23*HOUR)/1000,"BID odd record and genuine Sunday23UTC timestamp are selected, never ASK");
                }else failed(result,"malformed/incomplete/changed yearly source cannot publish "+label);
                check(routes.equals(label.startsWith("month-")?List.of(true,false):List.of(true)),"past-year route gets budget before unavailable monthly path; no hidden alternate after corrupt positive data");
            }
        }
        {
            var metadata=new BridgeHourlyRecoveryTest.Metadata();metadata.first.set(Instant.parse("2010-01-01T00:00:00Z").toEpochMilli());AtomicInteger calls=new AtomicInteger();
            long start=Instant.parse("2024-01-01T00:00:00Z").toEpochMilli(),finish=Instant.parse("2025-01-01T00:00:00Z").toEpochMilli(),from=Instant.parse("2024-11-20T00:00:00Z").toEpochMilli();
            Data[] payload=year(start,finish,from+HOUR);metadata.handler=fake(ICurvesProtocolHandler.class,(method,args)->{
                if(!method.equals("loadChunkFileFromCFG"))return null;calls.incrementAndGet();check(Boolean.TRUE.equals(args[4]),"cross-month export reuses a verified year source");return payload;
            });
            try(NativeFixture f=new NativeFixture(50,2_000,metadata.service(List.of()),NOW)){
                var reply=dispatch(f,query(Instrument.XAUUSD,from,from+40*DAY));
                for(int day=0;day<40;day++){f.load().result.complete(List.of());if(day==0)f.charts.load().finish(true);}
                JsonObject result=reply.get(3,TimeUnit.SECONDS);
                check(result.get("ok").getAsBoolean()&&result.getAsJsonArray("result").size()==1&&calls.get()==1&&f.charts.loads.isEmpty(),"one bounded yearly download supplies only requested months and proves genuine later-day absences without repeated downloads");
            }
        }
        for(String changed:List.of("default","metadata","account")){
            var metadata=new BridgeHourlyRecoveryTest.Metadata();metadata.first.set(Instant.parse("2010-01-01T00:00:00Z").toEpochMilli());AtomicInteger files=new AtomicInteger();
            long start=Instant.parse("2024-01-01T00:00:00Z").toEpochMilli(),finish=Instant.parse("2025-01-01T00:00:00Z").toEpochMilli(),from=Instant.parse("2024-11-30T00:00:00Z").toEpochMilli();
            Data[] payload=year(start,finish,from+HOUR);metadata.handler=fake(ICurvesProtocolHandler.class,(method,args)->{if(!method.equals("loadChunkFileFromCFG"))return null;files.incrementAndGet();return payload;});
            try(NativeFixture f=new NativeFixture(50,2_000,metadata.service(List.of()),NOW)){
                var reply=dispatch(f,query(Instrument.XAUUSD,from,from+2*DAY));f.load().result.complete(List.of());f.charts.load().finish(true);NativeLoad next=f.load();
                if(changed.equals("default"))f.charts.defaultFeed.set(null);
                if(changed.equals("metadata"))metadata.first.set(start-HOUR);
                if(changed.equals("account"))f.charts.accountId.set("different-account");
                next.result.complete(List.of());failed(reply.get(3,TimeUnit.SECONDS),"neighboring cached month cannot survive a between-day source mutation "+changed);
                check(files.get()==1&&f.charts.loads.isEmpty(),"changed cached source fails before another archive/download is used");
            }
        }
        System.out.println("JForex canonical past-year archive, paired BID validation, genuine Sundays, bounded fallback/cache and source/deadline checks passed");
    }
}
