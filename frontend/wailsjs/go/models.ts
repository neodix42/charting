export namespace candlestorage {
	
	export class Info {
	    enabled: boolean;
	    directory: string;
	    sizeBytes: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.directory = source["directory"];
	        this.sizeBytes = source["sizeBytes"];
	        this.error = source["error"];
	    }
	}

}

export namespace domain {
	
	export class Candle {
	    time: number;
	    open: number;
	    high: number;
	    low: number;
	    close: number;
	    volume: number;
	    closed: boolean;
	    quality: string;
	    sourceRevision: number;
	
	    static createFrom(source: any = {}) {
	        return new Candle(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.open = source["open"];
	        this.high = source["high"];
	        this.low = source["low"];
	        this.close = source["close"];
	        this.volume = source["volume"];
	        this.closed = source["closed"];
	        this.quality = source["quality"];
	        this.sourceRevision = source["sourceRevision"];
	    }
	}
	export class DrawingPoint {
	    time: number;
	    price: number;
	
	    static createFrom(source: any = {}) {
	        return new DrawingPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.price = source["price"];
	    }
	}
	export class Drawing {
	    id: string;
	    type: string;
	    exchange: string;
	    market: string;
	    instrument: string;
	    points: DrawingPoint[];
	
	    static createFrom(source: any = {}) {
	        return new Drawing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.exchange = source["exchange"];
	        this.market = source["market"];
	        this.instrument = source["instrument"];
	        this.points = this.convertValues(source["points"], DrawingPoint);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Indicator {
	    id: string;
	    type: string;
	    parameters: Record<string, number>;
	    color?: string;
	    lineWidth?: number;
	    paneStretchFactor?: number;
	
	    static createFrom(source: any = {}) {
	        return new Indicator(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.parameters = source["parameters"];
	        this.color = source["color"];
	        this.lineWidth = source["lineWidth"];
	        this.paneStretchFactor = source["paneStretchFactor"];
	    }
	}
	export class View {
	    mode: string;
	    fromSec?: number;
	    toSec?: number;
	    barSpacing: number;
	    rightOffset: number;
	
	    static createFrom(source: any = {}) {
	        return new View(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.fromSec = source["fromSec"];
	        this.toSec = source["toSec"];
	        this.barSpacing = source["barSpacing"];
	        this.rightOffset = source["rightOffset"];
	    }
	}
	export class PriceRange {
	    min: number;
	    max: number;
	
	    static createFrom(source: any = {}) {
	        return new PriceRange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	    }
	}
	export class PriceScale {
	    mode: string;
	    autoScale: boolean;
	    manualRange?: PriceRange;
	
	    static createFrom(source: any = {}) {
	        return new PriceScale(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.autoScale = source["autoScale"];
	        this.manualRange = this.convertValues(source["manualRange"], PriceRange);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Chart {
	    id: string;
	    exchange: string;
	    market: string;
	    instrument: string;
	    timeframe: string;
	    chartType: string;
	    showVolume: boolean;
	    priceScale: PriceScale;
	    view: View;
	    indicators?: Indicator[];
	    drawings?: Drawing[];
	
	    static createFrom(source: any = {}) {
	        return new Chart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.exchange = source["exchange"];
	        this.market = source["market"];
	        this.instrument = source["instrument"];
	        this.timeframe = source["timeframe"];
	        this.chartType = source["chartType"];
	        this.showVolume = source["showVolume"];
	        this.priceScale = this.convertValues(source["priceScale"], PriceScale);
	        this.view = this.convertValues(source["view"], View);
	        this.indicators = this.convertValues(source["indicators"], Indicator);
	        this.drawings = this.convertValues(source["drawings"], Drawing);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ChartRightGap {
	    unit: string;
	    value: number;
	
	    static createFrom(source: any = {}) {
	        return new ChartRightGap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.unit = source["unit"];
	        this.value = source["value"];
	    }
	}
	
	
	export class FeedKey {
	    exchange: string;
	    market: string;
	    instrument: string;
	    timeframe: string;
	
	    static createFrom(source: any = {}) {
	        return new FeedKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exchange = source["exchange"];
	        this.market = source["market"];
	        this.instrument = source["instrument"];
	        this.timeframe = source["timeframe"];
	    }
	}
	export class Grid {
	    rows: number;
	    columns: number;
	    rowFractions: number[];
	    columnFractions: number[];
	
	    static createFrom(source: any = {}) {
	        return new Grid(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rows = source["rows"];
	        this.columns = source["columns"];
	        this.rowFractions = source["rowFractions"];
	        this.columnFractions = source["columnFractions"];
	    }
	}
	export class Health {
	    state: string;
	    reason: string;
	    lastMessageAt: number;
	    lastTradeAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Health(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.reason = source["reason"];
	        this.lastMessageAt = source["lastMessageAt"];
	        this.lastTradeAt = source["lastTradeAt"];
	    }
	}
	
	export class Instrument {
	    nativeId: string;
	    market: string;
	    baseAsset: string;
	    quoteAsset: string;
	    priceIncrement: string;
	    sizeIncrement: string;
	    availability: string;
	    supportedIntervals: string[];
	    providerMetadata: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new Instrument(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nativeId = source["nativeId"];
	        this.market = source["market"];
	        this.baseAsset = source["baseAsset"];
	        this.quoteAsset = source["quoteAsset"];
	        this.priceIncrement = source["priceIncrement"];
	        this.sizeIncrement = source["sizeIncrement"];
	        this.availability = source["availability"];
	        this.supportedIntervals = source["supportedIntervals"];
	        this.providerMetadata = source["providerMetadata"];
	    }
	}
	
	
	export class Tab {
	    id: string;
	    name: string;
	    grid: Grid;
	    charts: Chart[];
	
	    static createFrom(source: any = {}) {
	        return new Tab(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.grid = this.convertValues(source["grid"], Grid);
	        this.charts = this.convertValues(source["charts"], Chart);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Window {
	    width: number;
	    height: number;
	    maximized: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Window(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	        this.maximized = source["maximized"];
	    }
	}
	export class Workspace {
	    schemaVersion: number;
	    app: string;
	    id: string;
	    theme: string;
	    fontSize?: number;
	    displayTimezone: string;
	    chartRightGap?: ChartRightGap;
	    activeTabId: string;
	    window: Window;
	    tabs: Tab[];
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.app = source["app"];
	        this.id = source["id"];
	        this.theme = source["theme"];
	        this.fontSize = source["fontSize"];
	        this.displayTimezone = source["displayTimezone"];
	        this.chartRightGap = this.convertValues(source["chartRightGap"], ChartRightGap);
	        this.activeTabId = source["activeTabId"];
	        this.window = this.convertValues(source["window"], Window);
	        this.tabs = this.convertValues(source["tabs"], Tab);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace jforex {

	export class Info {
	    username: string;
	    environment: string;
	    passwordConfigured: boolean;
	    runtimeAvailable: boolean;
	    runtimeDirectory: string;
	    javaPath: string;
	    connected: boolean;
	    instrumentCount: number;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.environment = source["environment"];
	        this.passwordConfigured = source["passwordConfigured"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeDirectory = source["runtimeDirectory"];
	        this.javaPath = source["javaPath"];
	        this.connected = source["connected"];
	        this.instrumentCount = source["instrumentCount"];
	        this.error = source["error"];
	    }
	}
	export class LoginChallenge {
	    imageDataUrl: string;

	    static createFrom(source: any = {}) {
	        return new LoginChallenge(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imageDataUrl = source["imageDataUrl"];
	    }
	}
	export class Settings {
	    username: string;
	    password: string;
	    environment: string;
	    clearPassword: boolean;

	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.password = source["password"];
	        this.environment = source["environment"];
	        this.clearPassword = source["clearPassword"];
	    }
	}

}

export namespace market {
	
	export class History {
	    requestId: string;
	    feedKey: domain.FeedKey;
	    bars: domain.Candle[];
	    moreHistory: boolean;
	
	    static createFrom(source: any = {}) {
	        return new History(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestId = source["requestId"];
	        this.feedKey = this.convertValues(source["feedKey"], domain.FeedKey);
	        this.bars = this.convertValues(source["bars"], domain.Candle);
	        this.moreHistory = source["moreHistory"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Snapshot {
	    leaseId: string;
	    revision: number;
	    bars: domain.Candle[];
	    health: domain.Health;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.leaseId = source["leaseId"];
	        this.revision = source["revision"];
	        this.bars = this.convertValues(source["bars"], domain.Candle);
	        this.health = this.convertValues(source["health"], domain.Health);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace predictions {

	export class Dataset {
	    id: string;
	    symbol: string;
	    name: string;
	    period: string;
	    filename: string;

	    static createFrom(source: any = {}) {
	        return new Dataset(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.symbol = source["symbol"];
	        this.name = source["name"];
	        this.period = source["period"];
	        this.filename = source["filename"];
	    }
	}
	export class Event {
	    phase: string;
	    progress: number;
	    message: string;
	    downloaded?: number;
	    total?: number;

	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.progress = source["progress"];
	        this.message = source["message"];
	        this.downloaded = source["downloaded"];
	        this.total = source["total"];
	    }
	}
	export class Point {
	    step: number;
	    lower: number;
	    median: number;
	    upper: number;

	    static createFrom(source: any = {}) {
	        return new Point(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.step = source["step"];
	        this.lower = source["lower"];
	        this.median = source["median"];
	        this.upper = source["upper"];
	    }
	}
	export class Observation {
	    time: number;
	    close: number;

	    static createFrom(source: any = {}) {
	        return new Observation(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.close = source["close"];
	    }
	}
	export class Forecast {
	    id: string;
	    createdAt: number;
	    asOf: number;
	    model: string;
	    modelRevision: string;
	    symbol: string;
	    assetName: string;
	    period: string;
	    contextLength: number;
	    horizon: number;
	    sourceFile: string;
	    sourceHash: string;
	    history: Observation[];
	    points: Point[];

	    static createFrom(source: any = {}) {
	        return new Forecast(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.createdAt = source["createdAt"];
	        this.asOf = source["asOf"];
	        this.model = source["model"];
	        this.modelRevision = source["modelRevision"];
	        this.symbol = source["symbol"];
	        this.assetName = source["assetName"];
	        this.period = source["period"];
	        this.contextLength = source["contextLength"];
	        this.horizon = source["horizon"];
	        this.sourceFile = source["sourceFile"];
	        this.sourceHash = source["sourceHash"];
	        this.history = this.convertValues(source["history"], Observation);
	        this.points = this.convertValues(source["points"], Point);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ForecastSummary {
	    id: string;
	    createdAt: number;
	    asOf: number;
	    model: string;
	    symbol: string;
	    assetName: string;
	    period: string;
	    contextLength: number;
	    horizon: number;

	    static createFrom(source: any = {}) {
	        return new ForecastSummary(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.createdAt = source["createdAt"];
	        this.asOf = source["asOf"];
	        this.model = source["model"];
	        this.symbol = source["symbol"];
	        this.assetName = source["assetName"];
	        this.period = source["period"];
	        this.contextLength = source["contextLength"];
	        this.horizon = source["horizon"];
	    }
	}
	export class RuntimeStatus {
	    ready: boolean;
	    modelReady: boolean;
	    runtimeReady: boolean;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new RuntimeStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ready = source["ready"];
	        this.modelReady = source["modelReady"];
	        this.runtimeReady = source["runtimeReady"];
	        this.error = source["error"];
	    }
	}
	export class Selection {
	    asset: string;
	    period: string;
	    contextLength: number;
	    horizon: number;
	    forecastId: string;

	    static createFrom(source: any = {}) {
	        return new Selection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.asset = source["asset"];
	        this.period = source["period"];
	        this.contextLength = source["contextLength"];
	        this.horizon = source["horizon"];
	        this.forecastId = source["forecastId"];
	    }
	}
	export class ModelOption {
	    id: string;
	    name: string;

	    static createFrom(source: any = {}) {
	        return new ModelOption(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class Settings {
	    model: string;
	    modelDirectory: string;

	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.modelDirectory = source["modelDirectory"];
	    }
	}
	export class Info {
	    settings: Settings;
	    models: ModelOption[];
	    defaultModelDirectory: string;
	    forecastDirectory: string;
	    selection: Selection;
	    runtime: RuntimeStatus;
	    active: boolean;
	    event: Event;
	    error?: string;
	    forecasts: ForecastSummary[];

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.settings = this.convertValues(source["settings"], Settings);
	        this.models = this.convertValues(source["models"], ModelOption);
	        this.defaultModelDirectory = source["defaultModelDirectory"];
	        this.forecastDirectory = source["forecastDirectory"];
	        this.selection = this.convertValues(source["selection"], Selection);
	        this.runtime = this.convertValues(source["runtime"], RuntimeStatus);
	        this.active = source["active"];
	        this.event = this.convertValues(source["event"], Event);
	        this.error = source["error"];
	        this.forecasts = this.convertValues(source["forecasts"], ForecastSummary);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}






}

export namespace screener {

	export class AnalysisProfitableMatch {
	    index: number;
	    time: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisProfitableMatch(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.time = source["time"];
	    }
	}

	export class AnalysisBar {
	    index: number;
	    time: number;
	    values: Record<string, number>;

	    static createFrom(source: any = {}) {
	        return new AnalysisBar(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.time = source["time"];
	        this.values = source["values"];
	    }
	}
	export class DataFile {
	    id: string;
	    name: string;
	    symbol?: string;
	    description: string;
	    category: string;
	    timeframe?: string;
	    from?: string;
	    to?: string;
	    firstTimestamp?: number;
	    lastTimestamp?: number;
	    fields: string[];
	    bytes: number;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new DataFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.symbol = source["symbol"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.firstTimestamp = source["firstTimestamp"];
	        this.lastTimestamp = source["lastTimestamp"];
	        this.fields = source["fields"];
	        this.bytes = source["bytes"];
	        this.error = source["error"];
	    }
	}
	export class AnalysisCatalog {
	    directory: string;
	    fields: string[];
	    timeframes: string[];
	    files: DataFile[];
	    categories: string[];

	    static createFrom(source: any = {}) {
	        return new AnalysisCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.fields = source["fields"];
	        this.timeframes = source["timeframes"];
	        this.files = this.convertValues(source["files"], DataFile);
	        this.categories = source["categories"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalysisTradeLevels {
	    entry: number;
	    stopLoss: number;
	    takeProfit: number;
	
	    static createFrom(source: any = {}) {
	        return new AnalysisTradeLevels(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entry = source["entry"];
	        this.stopLoss = source["stopLoss"];
	        this.takeProfit = source["takeProfit"];
	    }
	}

	export class AnalysisChart {
	    resultId: string;
	    name: string;
	    symbol: string;
	    timeframe: string;
	    occurrence: number;
	    totalMatches: number;
	    matchTime: number;
	    matchIndex: number;
	    startIndex: number;
	    fields: string[];
	    bars: AnalysisBar[];
	    profitableMatches?: AnalysisProfitableMatch[];
	    trade?: AnalysisTradeLevels;

	    static createFrom(source: any = {}) {
	        return new AnalysisChart(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.resultId = source["resultId"];
	        this.name = source["name"];
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.occurrence = source["occurrence"];
	        this.totalMatches = source["totalMatches"];
	        this.matchTime = source["matchTime"];
	        this.matchIndex = source["matchIndex"];
	        this.startIndex = source["startIndex"];
	        this.fields = source["fields"];
	        this.bars = this.convertValues(source["bars"], AnalysisBar);
	        this.profitableMatches = this.convertValues(source["profitableMatches"], AnalysisProfitableMatch);
	        this.trade = this.convertValues(source["trade"], AnalysisTradeLevels);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalysisOperand {
	    kind: string;
	    field: string;
	    value: number;
	    timeframe: string;
	    offset: number;
	    aggregate: string;
	    window: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisOperand(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.field = source["field"];
	        this.value = source["value"];
	        this.timeframe = source["timeframe"];
	        this.offset = source["offset"];
	        this.aggregate = source["aggregate"];
	        this.window = source["window"];
	    }
	}
	export class AnalysisCondition {
	    id: string;
	    operator: string;
	    comparison: string;
	    left: AnalysisOperand;
	    right: AnalysisOperand;
	    upper: AnalysisOperand;
	    distance: AnalysisOperand;
	    multiplier: number;
	    bars: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisCondition(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.operator = source["operator"];
	        this.comparison = source["comparison"];
	        this.left = this.convertValues(source["left"], AnalysisOperand);
	        this.right = this.convertValues(source["right"], AnalysisOperand);
	        this.upper = this.convertValues(source["upper"], AnalysisOperand);
	        this.distance = this.convertValues(source["distance"], AnalysisOperand);
	        this.multiplier = source["multiplier"];
	        this.bars = source["bars"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalysisFilter {
	    categories: string[];
	    includeEmpty: boolean;
	    profitableSort?: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisFilter(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.categories = source["categories"];
	        this.includeEmpty = source["includeEmpty"];
	        this.profitableSort = source["profitableSort"];
	    }
	}
	export class AnalysisTradeSimulation {
	    direction?: string;
	    stopLoss: string;
	    stopLossBarsAgo?: number;
	    takeProfit: string;
	    takeProfitMultiple?: number;
	    duration: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisTradeSimulation(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.direction = source["direction"];
	        this.stopLoss = source["stopLoss"];
	        this.stopLossBarsAgo = source["stopLossBarsAgo"];
	        this.takeProfit = source["takeProfit"];
	        this.takeProfitMultiple = source["takeProfitMultiple"];
	        this.duration = source["duration"];
	    }
	}
	export class AnalysisSimulationSummary {
	    takeProfitHits: number;
	    stopLossHits: number;
	    expired: number;
	    insufficientData: number;
	    invalidLevels: number;
	    unavailable: number;
	    profitablePercent: number;
	    reason?: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisSimulationSummary(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.takeProfitHits = source["takeProfitHits"];
	        this.stopLossHits = source["stopLossHits"];
	        this.expired = source["expired"];
	        this.insufficientData = source["insufficientData"];
	        this.invalidLevels = source["invalidLevels"];
	        this.unavailable = source["unavailable"];
	        this.profitablePercent = source["profitablePercent"];
	        this.reason = source["reason"];
	    }
	}
	export class AnalysisResult {
	    id: string;
	    fileId: string;
	    name: string;
	    symbol: string;
	    description: string;
	    category: string;
	    timeframe: string;
	    status: string;
	    error?: string;
	    rows: number;
	    matches: number;
	    assetMatches: number;
	    assetFiles: number;
	    firstTime?: number;
	    lastTime?: number;
	    simulation?: AnalysisSimulationSummary;

	    static createFrom(source: any = {}) {
	        return new AnalysisResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileId = source["fileId"];
	        this.name = source["name"];
	        this.symbol = source["symbol"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.timeframe = source["timeframe"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.rows = source["rows"];
	        this.matches = source["matches"];
	        this.assetMatches = source["assetMatches"];
	        this.assetFiles = source["assetFiles"];
	        this.firstTime = source["firstTime"];
	        this.lastTime = source["lastTime"];
	        this.simulation = this.convertValues(source["simulation"], AnalysisSimulationSummary);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalysisInfo {
	    active: boolean;
	    phase: string;
	    total: number;
	    completed: number;
	    failed: number;
	    currentFile: string;
	    progress: number;
	    rows: number;
	    totalMatches: number;
	    matchedFiles: number;
	    results: AnalysisResult[];
	    offset: number;
	    limit: number;
	    totalResults: number;
	    error?: string;
	    tradeSimulation?: AnalysisTradeSimulation;

	    static createFrom(source: any = {}) {
	        return new AnalysisInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active = source["active"];
	        this.phase = source["phase"];
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.currentFile = source["currentFile"];
	        this.progress = source["progress"];
	        this.rows = source["rows"];
	        this.totalMatches = source["totalMatches"];
	        this.matchedFiles = source["matchedFiles"];
	        this.results = this.convertValues(source["results"], AnalysisResult);
	        this.offset = source["offset"];
	        this.limit = source["limit"];
	        this.totalResults = source["totalResults"];
	        this.error = source["error"];
	        this.tradeSimulation = this.convertValues(source["tradeSimulation"], AnalysisTradeSimulation);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class AnalysisRequest {
	    conditions: AnalysisCondition[];
	    period?: string;
	    categories: string[];
	    tradeSimulation?: AnalysisTradeSimulation;

	    static createFrom(source: any = {}) {
	        return new AnalysisRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conditions = this.convertValues(source["conditions"], AnalysisCondition);
	        this.period = source["period"];
	        this.categories = source["categories"];
	        this.tradeSimulation = this.convertValues(source["tradeSimulation"], AnalysisTradeSimulation);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class Instrument {
	    id: string;
	    name: string;
	    earliestDate: string;
	
	    static createFrom(source: any = {}) {
	        return new Instrument(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.earliestDate = source["earliestDate"];
	    }
	}
	export class Group {
	    name: string;
	    instruments: Instrument[];
	
	    static createFrom(source: any = {}) {
	        return new Group(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.instruments = this.convertValues(source["instruments"], Instrument);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Catalog {
	    version: string;
	    groups: Group[];
	    timeframes: string[];
	
	    static createFrom(source: any = {}) {
	        return new Catalog(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.groups = this.convertValues(source["groups"], Group);
	        this.timeframes = source["timeframes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class EnrichmentJob {
	    id: string;
	    fileId: string;
	    name: string;
	    status: string;
	    progress: number;
	    rows: number;
	    message: string;
	    columns: string[];
	    backupPath?: string;

	    static createFrom(source: any = {}) {
	        return new EnrichmentJob(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileId = source["fileId"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.rows = source["rows"];
	        this.message = source["message"];
	        this.columns = source["columns"];
	        this.backupPath = source["backupPath"];
	    }
	}
	export class EnrichmentInfo {
	    directory: string;
	    active: boolean;
	    runtimeAvailable: boolean;
	    runtimeError?: string;
	    indicator: string;
	    total: number;
	    completed: number;
	    failed: number;
	    cancelled: number;
	    progress: number;
	    currentFile: string;
	    currentProgress: number;
	    jobs: EnrichmentJob[];

	    static createFrom(source: any = {}) {
	        return new EnrichmentInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.active = source["active"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeError = source["runtimeError"];
	        this.indicator = source["indicator"];
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.cancelled = source["cancelled"];
	        this.progress = source["progress"];
	        this.currentFile = source["currentFile"];
	        this.currentProgress = source["currentProgress"];
	        this.jobs = this.convertValues(source["jobs"], EnrichmentJob);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class EnrichmentRequest {
	    indicator: string;
	    parameters: Record<string, number>;
	    source: string;
	    period?: string;

	    static createFrom(source: any = {}) {
	        return new EnrichmentRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.indicator = source["indicator"];
	        this.parameters = source["parameters"];
	        this.source = source["source"];
	        this.period = source["period"];
	    }
	}
	export class FilePage {
	    directory: string;
	    files: DataFile[];
	    total: number;
	    offset: number;
	    limit: number;
	    periodCounts?: Record<string, number>;

	    static createFrom(source: any = {}) {
	        return new FilePage(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.files = this.convertValues(source["files"], DataFile);
	        this.total = source["total"];
	        this.offset = source["offset"];
	        this.limit = source["limit"];
	        this.periodCounts = source["periodCounts"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class IndicatorParameter {
	    key: string;
	    label: string;
	    default: number;
	    min: number;
	    max: number;
	    step: number;

	    static createFrom(source: any = {}) {
	        return new IndicatorParameter(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.default = source["default"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.step = source["step"];
	    }
	}
	export class Indicator {
	    id: string;
	    name: string;
	    category: string;
	    input: string;
	    parameters: IndicatorParameter[];
	    note?: string;

	    static createFrom(source: any = {}) {
	        return new Indicator(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.input = source["input"];
	        this.parameters = this.convertValues(source["parameters"], IndicatorParameter);
	        this.note = source["note"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class IndicatorCatalog {
	    version: string;
	    indicators: Indicator[];

	    static createFrom(source: any = {}) {
	        return new IndicatorCatalog(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.indicators = this.convertValues(source["indicators"], Indicator);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class Job {
	    id: string;
	    symbol: string;
	    timeframe: string;
	    from: string;
	    to: string;
	    status: string;
	    progress: number;
	    message: string;
	    outputPath?: string;
	    asOf?: number;
	
	    static createFrom(source: any = {}) {
	        return new Job(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.message = source["message"];
	        this.outputPath = source["outputPath"];
	        this.asOf = source["asOf"];
	    }
	}
	export class Info {
	    directory: string;
	    runtimeAvailable: boolean;
	    runtimeError?: string;
	    error?: string;
	    jobs: Job[];
	    downloadedSymbols: string[];
	    concurrency: number;
	    maxConcurrency: number;
	    active: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeError = source["runtimeError"];
	        this.error = source["error"];
	        this.jobs = this.convertValues(source["jobs"], Job);
	        this.downloadedSymbols = source["downloadedSymbols"];
	        this.concurrency = source["concurrency"];
	        this.maxConcurrency = source["maxConcurrency"];
	        this.active = source["active"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class RefreshJob {
	    id: string;
	    fileId: string;
	    name: string;
	    status: string;
	    progress: number;
	    rows: number;
	    addedRows: number;
	    updatedRows: number;
	    message: string;
	    columns: string[];
	    backupPath?: string;

	    static createFrom(source: any = {}) {
	        return new RefreshJob(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileId = source["fileId"];
	        this.name = source["name"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.rows = source["rows"];
	        this.addedRows = source["addedRows"];
	        this.updatedRows = source["updatedRows"];
	        this.message = source["message"];
	        this.columns = source["columns"];
	        this.backupPath = source["backupPath"];
	    }
	}
	export class RefreshInfo {
	    directory: string;
	    active: boolean;
	    asOf?: number;
	    runtimeAvailable: boolean;
	    runtimeError?: string;
	    total: number;
	    completed: number;
	    failed: number;
	    cancelled: number;
	    progress: number;
	    currentFile: string;
	    currentProgress: number;
	    jobs: RefreshJob[];

	    static createFrom(source: any = {}) {
	        return new RefreshInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.active = source["active"];
	        this.asOf = source["asOf"];
	        this.runtimeAvailable = source["runtimeAvailable"];
	        this.runtimeError = source["runtimeError"];
	        this.total = source["total"];
	        this.completed = source["completed"];
	        this.failed = source["failed"];
	        this.cancelled = source["cancelled"];
	        this.progress = source["progress"];
	        this.currentFile = source["currentFile"];
	        this.currentProgress = source["currentProgress"];
	        this.jobs = this.convertValues(source["jobs"], RefreshJob);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class Request {
	    symbols: string[];
	    timeframe: string;
	    from: string;
	    to: string;
	    concurrency?: number;
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbols = source["symbols"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.concurrency = source["concurrency"];
	    }
	}
	export class SavedAnalysisCondition {
	    id: string;
	    name: string;
	    request: AnalysisRequest;

	    static createFrom(source: any = {}) {
	        return new SavedAnalysisCondition(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.request = this.convertValues(source["request"], AnalysisRequest);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ViewState {
	    symbols: string[];
	    timeframe: string;
	    from: string;
	    to: string;
	    concurrency: number;
	    collapsedCategories: string[];

	    static createFrom(source: any = {}) {
	        return new ViewState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbols = source["symbols"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.concurrency = source["concurrency"];
	        this.collapsedCategories = source["collapsedCategories"];
	    }
	}

	export class LiveAlertAsset {
	    symbol: string;
	    name: string;
	    category: string;
	    periods: string[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertAsset(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbol = source["symbol"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.periods = source["periods"];
	    }
	}

	export class LiveAlertOutcome {
	    conditionName: string;
	    symbol: string;
	    assetName: string;
	    timeframe: string;
	    time: number;
	    status: string;
	    message: string;

	    static createFrom(source: any = {}) {
	        return new LiveAlertOutcome(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conditionName = source["conditionName"];
	        this.symbol = source["symbol"];
	        this.assetName = source["assetName"];
	        this.timeframe = source["timeframe"];
	        this.time = source["time"];
	        this.status = source["status"];
	        this.message = source["message"];
	    }
	}

	export class LiveAlertConditionPeriod {
	    conditionId: string;
	    period: string;

	    static createFrom(source: any = {}) {
	        return new LiveAlertConditionPeriod(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conditionId = source["conditionId"];
	        this.period = source["period"];
	    }
	}

	export class LiveAlertsConfig {
	    enabled: boolean;
	    conditionPeriods: LiveAlertConditionPeriod[];
	    symbols: string[];
	    conditionIds?: string[];
	    periods?: string[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertsConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.conditionPeriods = this.convertValues(source["conditionPeriods"], LiveAlertConditionPeriod);
	        this.symbols = source["symbols"];
	        this.conditionIds = source["conditionIds"];
	        this.periods = source["periods"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class LiveAlertsInfo {
	    config: LiveAlertsConfig;
	    running: boolean;
	    lastRun: number;
	    nextRun: number;
	    sent: number;
	    error: string;
	    status: string;
	    progress: number;
	    currentAsset: string;
	    assets: LiveAlertAsset[];
	    recent: LiveAlertOutcome[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertsInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = this.convertValues(source["config"], LiveAlertsConfig);
	        this.running = source["running"];
	        this.lastRun = source["lastRun"];
	        this.nextRun = source["nextRun"];
	        this.sent = source["sent"];
	        this.error = source["error"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.currentAsset = source["currentAsset"];
	        this.assets = this.convertValues(source["assets"], LiveAlertAsset);
	        this.recent = this.convertValues(source["recent"], LiveAlertOutcome);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
}

export namespace storage {
	
	export class Ack {
	    revision: number;
	    acceptedEditSequence: number;
	
	    static createFrom(source: any = {}) {
	        return new Ack(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.revision = source["revision"];
	        this.acceptedEditSequence = source["acceptedEditSequence"];
	    }
	}
	export class Backup {
	    id: number;
	    revision: number;
	    createdAt: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new Backup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.revision = source["revision"];
	        this.createdAt = source["createdAt"];
	        this.reason = source["reason"];
	    }
	}
	export class Loaded {
	    workspace: domain.Workspace;
	    revision: number;
	    recovery: string;
	
	    static createFrom(source: any = {}) {
	        return new Loaded(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = this.convertValues(source["workspace"], domain.Workspace);
	        this.revision = source["revision"];
	        this.recovery = source["recovery"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}


export namespace telegram {

	export class BotIdentity {
	    id: number;
	    username: string;

	    static createFrom(source: any = {}) {
	        return new BotIdentity(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.username = source["username"];
	    }
	}
	export class Settings {
	    apiToken: string;
	    recipient: string;

	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.apiToken = source["apiToken"];
	        this.recipient = source["recipient"];
	    }
	}
	export class TestResult {
	    sent: boolean;
	    code?: string;
	    botUsername?: string;
	    messageId?: number;

	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sent = source["sent"];
	        this.code = source["code"];
	        this.botUsername = source["botUsername"];
	        this.messageId = source["messageId"];
	    }
	}

}
