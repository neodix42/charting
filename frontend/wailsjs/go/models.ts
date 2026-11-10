export namespace candlestorage {
	
	export class Info {
	    enabled: boolean;
	    directory: string;
	    sizeBytes: number;
	    error?: string;
	    pending?: boolean;

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.directory = source["directory"];
	        this.sizeBytes = source["sizeBytes"];
	        this.error = source["error"];
	        this.pending = source["pending"];
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
	    visibility?: Record<string, boolean>;

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
	        this.visibility = source["visibility"];
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
	export class Quote {
	    bid?: number;
	    ask?: number;
	    time: number;

	    static createFrom(source: any = {}) {
	        return new Quote(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bid = source["bid"];
	        this.ask = source["ask"];
	        this.time = source["time"];
	    }
	}
	export class Health {
	    quote?: Quote;
	    state: string;
	    reason: string;
	    lastMessageAt: number;
	    lastTradeAt: number;

	    static createFrom(source: any = {}) {
	        return new Health(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.quote = this.convertValues(source["quote"], Quote);
	        this.state = source["state"];
	        this.reason = source["reason"];
	        this.lastMessageAt = source["lastMessageAt"];
	        this.lastTradeAt = source["lastTradeAt"];
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

	export class LiveAlertAssetRef {
	    market?: string;
	    symbol: string;

	    static createFrom(source: any = {}) {
	        return new LiveAlertAssetRef(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.market = source["market"];
	        this.symbol = source["symbol"];
	    }
	}

	export class LiveAlertAssetSelection {
	    dataSource: string;
	    assets: LiveAlertAssetRef[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertAssetSelection(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataSource = source["dataSource"];
	        this.assets = this.convertValues(source["assets"], LiveAlertAssetRef);
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

	export class LiveAlertDefinition {
	    id: string;
	    name: string;
	    conditionId: string;
	    period: string;
	    dataSource: string;
	    enabled: boolean;
	    assetSelections?: LiveAlertAssetSelection[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertDefinition(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.conditionId = source["conditionId"];
	        this.period = source["period"];
	        this.dataSource = source["dataSource"];
	        this.enabled = source["enabled"];
	        this.assetSelections = this.convertValues(source["assetSelections"], LiveAlertAssetSelection);
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

	export class LiveAlertConditionSource {
	    conditionId: string;
	    source: string;

	    static createFrom(source: any = {}) {
	        return new LiveAlertConditionSource(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conditionId = source["conditionId"];
	        this.source = source["source"];
	    }
	}

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
	    dataSource: string;
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
	        this.dataSource = source["dataSource"];
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
	    dataSource: string;
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
	        this.dataSource = source["dataSource"];
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
	export class AnalysisTradeExecution {
	    entryTime: number;
	    outcome: string;
	    exitTime?: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisTradeExecution(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entryTime = source["entryTime"];
	        this.outcome = source["outcome"];
	        this.exitTime = source["exitTime"];
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
	    tradeExecution?: AnalysisTradeExecution;
	    signal?: AnalysisSignalLevels;

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
	        this.tradeExecution = this.convertValues(source["tradeExecution"], AnalysisTradeExecution);
	        this.signal = this.convertValues(source["signal"], AnalysisSignalLevels);
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
	    profitabilityFilter?: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisFilter(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.categories = source["categories"];
	        this.includeEmpty = source["includeEmpty"];
	        this.profitableSort = source["profitableSort"];
	        this.profitabilityFilter = source["profitabilityFilter"];
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
	    dataSource: string;
	    profitabilityFilter: number;
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
	        this.dataSource = source["dataSource"];
	        this.profitabilityFilter = source["profitabilityFilter"];
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
	    dataSource?: string;
	    profitabilityFilter?: number;
	    period?: string;
	    categories: string[];
	    tradeSimulation?: AnalysisTradeSimulation;

	    static createFrom(source: any = {}) {
	        return new AnalysisRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conditions = this.convertValues(source["conditions"], AnalysisCondition);
	        this.dataSource = source["dataSource"];
	        this.profitabilityFilter = source["profitabilityFilter"];
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
	    nativeId?: string;
	    market?: string;
	    id: string;
	    name: string;
	    earliestDate: string;

	    static createFrom(source: any = {}) {
	        return new Instrument(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nativeId = source["nativeId"];
	        this.market = source["market"];
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
	    dataSource?: string;
	    indicator: string;
	    parameters: Record<string, number>;
	    source: string;
	    period?: string;

	    static createFrom(source: any = {}) {
	        return new EnrichmentRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataSource = source["dataSource"];
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
	    allTotal: number;
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
	        this.allTotal = source["allTotal"];
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
	    dataSource?: string;
	    nativeId?: string;
	    market?: string;
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
	        this.dataSource = source["dataSource"];
	        this.nativeId = source["nativeId"];
	        this.market = source["market"];
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
	    dataSource?: string;
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
	        this.dataSource = source["dataSource"];
	        this.symbols = source["symbols"];
	        this.timeframe = source["timeframe"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.concurrency = source["concurrency"];
	    }
	}
	export class SavedConditionProfitability {
	    symbol: string;
	    timeframe: string;
	    fileName: string;
	    matches: number;
	    takeProfitHits: number;
	    profitablePercent: number;
	    profitabilityUnavailable?: boolean;

	    static createFrom(source: any = {}) {
	        return new SavedConditionProfitability(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbol = source["symbol"];
	        this.timeframe = source["timeframe"];
	        this.fileName = source["fileName"];
	        this.matches = source["matches"];
	        this.takeProfitHits = source["takeProfitHits"];
	        this.profitablePercent = source["profitablePercent"];
	        this.profitabilityUnavailable = source["profitabilityUnavailable"];
	    }
	}
	export class SavedConditionAnalysis {
	    analyzedAt: number;
	    assets: SavedConditionProfitability[];
	    failedFiles?: number;

	    static createFrom(source: any = {}) {
	        return new SavedConditionAnalysis(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.analyzedAt = source["analyzedAt"];
	        this.assets = this.convertValues(source["assets"], SavedConditionProfitability);
	        this.failedFiles = source["failedFiles"];
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
	export class SavedAnalysisCondition {
	    id: string;
	    name: string;
	    request: AnalysisRequest;
	    analysis?: SavedConditionAnalysis;

	    static createFrom(source: any = {}) {
	        return new SavedAnalysisCondition(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.request = this.convertValues(source["request"], AnalysisRequest);
	        this.analysis = this.convertValues(source["analysis"], SavedConditionAnalysis);
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
	    dataSource?: string;
	    hyperliquidSymbols?: string[];
	    hyperliquidSelectionInitialized?: boolean;
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
	        this.dataSource = source["dataSource"];
	        this.hyperliquidSymbols = source["hyperliquidSymbols"];
	        this.hyperliquidSelectionInitialized = source["hyperliquidSelectionInitialized"];
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

	export class LiveAlertTarget {
	    symbol: string;
	    name: string;
	    category: string;
	    period: string;
	    profitability?: SavedConditionProfitability;

	    static createFrom(source: any = {}) {
	        return new LiveAlertTarget(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.symbol = source["symbol"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.period = source["period"];
	        this.profitability = this.convertValues(source["profitability"], SavedConditionProfitability);
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
	export class LiveAlertConditionScope {
	    availableTargets?: LiveAlertTarget[];
	    alertId?: string;
	    savedConditionId?: string;
	    conditionId: string;
	    period: string;
	    targets: LiveAlertTarget[];
	    error: string;
	    dataSource?: string;
	    allAssets?: boolean;

	    static createFrom(source: any = {}) {
	        return new LiveAlertConditionScope(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.availableTargets = this.convertValues(source["availableTargets"], LiveAlertTarget);
	        this.alertId = source["alertId"];
	        this.savedConditionId = source["savedConditionId"];
	        this.conditionId = source["conditionId"];
	        this.period = source["period"];
	        this.targets = this.convertValues(source["targets"], LiveAlertTarget);
	        this.error = source["error"];
	        this.dataSource = source["dataSource"];
	        this.allAssets = source["allAssets"];
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
	export class LiveAlertOutcome {
	    alertId?: string;
	    alertName?: string;
	    savedConditionId?: string;
	    savedConditionName?: string;
	    conditionId?: string;
	    dataSource?: string;
	    market?: string;
	    confirmedRecipients?: number;
	    conditionName: string;
	    symbol: string;
	    assetName: string;
	    timeframe: string;
	    time: number;
	    status: string;
	    message: string;
	    profitability?: SavedConditionProfitability;

	    static createFrom(source: any = {}) {
	        return new LiveAlertOutcome(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.alertId = source["alertId"];
	        this.alertName = source["alertName"];
	        this.savedConditionId = source["savedConditionId"];
	        this.savedConditionName = source["savedConditionName"];
	        this.conditionId = source["conditionId"];
	        this.dataSource = source["dataSource"];
	        this.market = source["market"];
	        this.confirmedRecipients = source["confirmedRecipients"];
	        this.conditionName = source["conditionName"];
	        this.symbol = source["symbol"];
	        this.assetName = source["assetName"];
	        this.timeframe = source["timeframe"];
	        this.time = source["time"];
	        this.status = source["status"];
	        this.message = source["message"];
	        this.profitability = this.convertValues(source["profitability"], SavedConditionProfitability);
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
	    alerts: LiveAlertDefinition[];
	    enabled: boolean;
	    conditionIds: string[];
	    conditionSources?: LiveAlertConditionSource[];
	    conditionPeriods?: LiveAlertConditionPeriod[];
	    symbols?: string[];
	    periods?: string[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertsConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.alerts = this.convertValues(source["alerts"], LiveAlertDefinition);
	        this.enabled = source["enabled"];
	        this.conditionIds = source["conditionIds"];
	        this.conditionSources = this.convertValues(source["conditionSources"], LiveAlertConditionSource);
	        this.conditionPeriods = this.convertValues(source["conditionPeriods"], LiveAlertConditionPeriod);
	        this.symbols = source["symbols"];
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
	export class LiveAlertCheck {
	    progress?: number;
	    phase?: string;
	    currentAsset?: string;
	    progressState?: string;
	    progressMode?: string;
	    alertId?: string;
	    savedConditionId?: string;
	    conditionId: string;
	    name: string;
	    assetsScanned: number;
	    assetsSkipped?: number;
	    lastChecked: number;
	    nextCheck: number;
	    sentAlerts: number;
	    status: string;
	    manual: boolean;
	    error: string;
	    timingHint?: string;

	    static createFrom(source: any = {}) {
	        return new LiveAlertCheck(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.progress = source["progress"];
	        this.phase = source["phase"];
	        this.currentAsset = source["currentAsset"];
	        this.progressState = source["progressState"];
	        this.progressMode = source["progressMode"];
	        this.alertId = source["alertId"];
	        this.savedConditionId = source["savedConditionId"];
	        this.conditionId = source["conditionId"];
	        this.name = source["name"];
	        this.assetsScanned = source["assetsScanned"];
	        this.assetsSkipped = source["assetsSkipped"];
	        this.lastChecked = source["lastChecked"];
	        this.nextCheck = source["nextCheck"];
	        this.sentAlerts = source["sentAlerts"];
	        this.status = source["status"];
	        this.manual = source["manual"];
	        this.error = source["error"];
	        this.timingHint = source["timingHint"];
	    }
	}
	export class LiveAlertsInfo {
	    savedConditionsRevision: number;
	    config: LiveAlertsConfig;
	    conditionScopes: LiveAlertConditionScope[];
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
	    checks: LiveAlertCheck[];

	    static createFrom(source: any = {}) {
	        return new LiveAlertsInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.savedConditionsRevision = source["savedConditionsRevision"];
	        this.config = this.convertValues(source["config"], LiveAlertsConfig);
	        this.conditionScopes = this.convertValues(source["conditionScopes"], LiveAlertConditionScope);
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
	        this.checks = this.convertValues(source["checks"], LiveAlertCheck);
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

	export class AnalysisSignalLevels {
	    entry: number;
	    stopLoss?: number;
	    takeProfit?: number;

	    static createFrom(source: any = {}) {
	        return new AnalysisSignalLevels(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entry = source["entry"];
	        this.stopLoss = source["stopLoss"];
	        this.takeProfit = source["takeProfit"];
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
	    sentCount?: number;
	    totalRecipients?: number;

	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sent = source["sent"];
	        this.code = source["code"];
	        this.botUsername = source["botUsername"];
	        this.messageId = source["messageId"];
	        this.sentCount = source["sentCount"];
	        this.totalRecipients = source["totalRecipients"];
	    }
	}

}

export namespace hyperliquid {

	export class Info {
	    accountAddress: string;
	    agentAddress: string;
	    network: string;
	    privateKeyConfigured: boolean;
	    connected: boolean;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accountAddress = source["accountAddress"];
	        this.agentAddress = source["agentAddress"];
	        this.network = source["network"];
	        this.privateKeyConfigured = source["privateKeyConfigured"];
	        this.connected = source["connected"];
	        this.error = source["error"];
	    }
	}
	export class Settings {
	    accountAddress: string;
	    privateKey: string;
	    network: string;
	    clearPrivateKey: boolean;

	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accountAddress = source["accountAddress"];
	        this.privateKey = source["privateKey"];
	        this.network = source["network"];
	        this.clearPrivateKey = source["clearPrivateKey"];
	    }
	}


	export class TokenBalance {
	    token: number;
	    coin: string;
	    tokenId: string;
	    szDecimals: number;
	    weiDecimals: number;
	    total?: string;
	    held?: string;
	    available?: string;
	    equity?: string;
	    usedMargin?: string;
	    observedAt: number;
	    error?: string;
	    availableError?: string;
	    basis?: string;

	    static createFrom(source: any = {}) {
	        return new TokenBalance(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.token = source["token"];
	        this.coin = source["coin"];
	        this.tokenId = source["tokenId"];
	        this.szDecimals = source["szDecimals"];
	        this.weiDecimals = source["weiDecimals"];
	        this.total = source["total"];
	        this.held = source["held"];
	        this.available = source["available"];
	        this.equity = source["equity"];
	        this.usedMargin = source["usedMargin"];
	        this.observedAt = source["observedAt"];
	        this.error = source["error"];
	        this.availableError = source["availableError"];
	        this.basis = source["basis"];
	    }
	}

	export class VenueBalance {
	    dex: string;
	    name: string;
	    market: string;
	    collateralToken?: number;
	    balances: TokenBalance[];
	    shared: boolean;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new VenueBalance(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dex = source["dex"];
	        this.name = source["name"];
	        this.market = source["market"];
	        this.collateralToken = source["collateralToken"];
	        this.balances = this.convertValues(source["balances"], TokenBalance);
	        this.shared = source["shared"];
	        this.error = source["error"];
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

	export class AccountBalances {
	    accountAddress: string;
	    network: string;
	    accountMode: string;
	    shared: boolean;
	    observedAt: number;
	    venues: VenueBalance[];
	    tokens: TokenBalance[];
	    notice?: string;

	    static createFrom(source: any = {}) {
	        return new AccountBalances(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accountAddress = source["accountAddress"];
	        this.network = source["network"];
	        this.accountMode = source["accountMode"];
	        this.shared = source["shared"];
	        this.observedAt = source["observedAt"];
	        this.venues = this.convertValues(source["venues"], VenueBalance);
	        this.tokens = this.convertValues(source["tokens"], TokenBalance);
	        this.notice = source["notice"];
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

	export class TransferPreview {
	    id: string;
	    sourceDex: string;
	    sourceName: string;
	    destinationDex: string;
	    destinationName: string;
	    token: number;
	    coin: string;
	    tokenId: string;
	    amount: string;
	    availableBefore: string;
	    availableAfter: string;
	    observedAt: number;
	    expiresAt: number;
	    accountAddress: string;
	    network: string;
	    warnings: string[];

	    static createFrom(source: any = {}) {
	        return new TransferPreview(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sourceDex = source["sourceDex"];
	        this.sourceName = source["sourceName"];
	        this.destinationDex = source["destinationDex"];
	        this.destinationName = source["destinationName"];
	        this.token = source["token"];
	        this.coin = source["coin"];
	        this.tokenId = source["tokenId"];
	        this.amount = source["amount"];
	        this.availableBefore = source["availableBefore"];
	        this.availableAfter = source["availableAfter"];
	        this.observedAt = source["observedAt"];
	        this.expiresAt = source["expiresAt"];
	        this.accountAddress = source["accountAddress"];
	        this.network = source["network"];
	        this.warnings = source["warnings"];
	    }
	}

	export class TransferRequest {
	    sourceDex: string;
	    destinationDex: string;
	    token: number;
	    amount: string;

	    static createFrom(source: any = {}) {
	        return new TransferRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceDex = source["sourceDex"];
	        this.destinationDex = source["destinationDex"];
	        this.token = source["token"];
	        this.amount = source["amount"];
	    }
	}

	export class TransferResult {
	    id: string;
	    status: string;
	    message: string;

	    static createFrom(source: any = {}) {
	        return new TransferResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.status = source["status"];
	        this.message = source["message"];
	    }
	}
}

export namespace trading {

	export class Preview {
	    availableBalance?: string;
	    availableBalanceCurrency?: string;
	    availableBalanceError?: string;
	    availableBalanceBasis?: string;
	    accountBalance?: string;
	    equity?: string;
	    usedMargin?: string;
	    marginRequired?: string;
	    marginRequiredCurrency?: string;
	    marginError?: string;
	    marginBasis?: string;
	    liquidationPrice?: string;
	    liquidationError?: string;
	    liquidationBasis?: string;
	    liquidationKind?: string;
	    metricsTime?: number;
	    id: string;
	    feedKey: domain.FeedKey;
	    side: string;
	    quantity: string;
	    entryPrice: string;
	    stopLoss: string;
	    takeProfit: string;
	    leverage: number;
	    environment: string;
	    expiresAt: number;
	    warnings: string[];
	    accountCurrency?: string;
	    profitPerPriceUnit?: string;
	    valuationTime?: number;
	    valuationError?: string;
	    valuationSource?: string;
	    valuationNotice?: string;
	    maxLeverage?: number;

	    static createFrom(source: any = {}) {
	        return new Preview(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.availableBalance = source["availableBalance"];
	        this.availableBalanceCurrency = source["availableBalanceCurrency"];
	        this.availableBalanceError = source["availableBalanceError"];
	        this.availableBalanceBasis = source["availableBalanceBasis"];
	        this.accountBalance = source["accountBalance"];
	        this.equity = source["equity"];
	        this.usedMargin = source["usedMargin"];
	        this.marginRequired = source["marginRequired"];
	        this.marginRequiredCurrency = source["marginRequiredCurrency"];
	        this.marginError = source["marginError"];
	        this.marginBasis = source["marginBasis"];
	        this.liquidationPrice = source["liquidationPrice"];
	        this.liquidationError = source["liquidationError"];
	        this.liquidationBasis = source["liquidationBasis"];
	        this.liquidationKind = source["liquidationKind"];
	        this.metricsTime = source["metricsTime"];
	        this.id = source["id"];
	        this.feedKey = this.convertValues(source["feedKey"], domain.FeedKey);
	        this.side = source["side"];
	        this.quantity = source["quantity"];
	        this.entryPrice = source["entryPrice"];
	        this.stopLoss = source["stopLoss"];
	        this.takeProfit = source["takeProfit"];
	        this.leverage = source["leverage"];
	        this.environment = source["environment"];
	        this.expiresAt = source["expiresAt"];
	        this.warnings = source["warnings"];
	        this.accountCurrency = source["accountCurrency"];
	        this.profitPerPriceUnit = source["profitPerPriceUnit"];
	        this.valuationTime = source["valuationTime"];
	        this.valuationError = source["valuationError"];
	        this.valuationSource = source["valuationSource"];
	        this.valuationNotice = source["valuationNotice"];
	        this.maxLeverage = source["maxLeverage"];
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
	    feedKey: domain.FeedKey;
	    side: string;
	    quantity: string;
	    stopLoss: string;
	    takeProfit: string;
	    leverage: number;

	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.feedKey = this.convertValues(source["feedKey"], domain.FeedKey);
	        this.side = source["side"];
	        this.quantity = source["quantity"];
	        this.stopLoss = source["stopLoss"];
	        this.takeProfit = source["takeProfit"];
	        this.leverage = source["leverage"];
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

	export class Result {
	    status: string;
	    orderIds: string[];
	    message: string;

	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.orderIds = source["orderIds"];
	        this.message = source["message"];
	    }
	}

	export class Status {
	    configured: boolean;
	    enabled: boolean;
	    environment: string;
	    canBuy: boolean;
	    canSell: boolean;
	    leverageSupported: boolean;
	    protectiveOrdersSupported: boolean;
	    maxLeverage: number;
	    quantityLabel: string;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configured = source["configured"];
	        this.enabled = source["enabled"];
	        this.environment = source["environment"];
	        this.canBuy = source["canBuy"];
	        this.canSell = source["canSell"];
	        this.leverageSupported = source["leverageSupported"];
	        this.protectiveOrdersSupported = source["protectiveOrdersSupported"];
	        this.maxLeverage = source["maxLeverage"];
	        this.quantityLabel = source["quantityLabel"];
	        this.error = source["error"];
	    }
	}

	export class Estimate {
	    feedKey: domain.FeedKey;
	    side: string;
	    entryPrice: string;
	    accountCurrency: string;
	    profitPerPriceUnit: string;
	    valuationTime: number;
	    valuationError?: string;
	    valuationSource?: string;
	    valuationNotice?: string;
	    maxLeverage?: number;

	    static createFrom(source: any = {}) {
	        return new Estimate(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.feedKey = this.convertValues(source["feedKey"], domain.FeedKey);
	        this.side = source["side"];
	        this.entryPrice = source["entryPrice"];
	        this.accountCurrency = source["accountCurrency"];
	        this.profitPerPriceUnit = source["profitPerPriceUnit"];
	        this.valuationTime = source["valuationTime"];
	        this.valuationError = source["valuationError"];
	        this.valuationSource = source["valuationSource"];
	        this.valuationNotice = source["valuationNotice"];
	        this.maxLeverage = source["maxLeverage"];
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

	export class Metrics {
	    availableBalance?: string;
	    availableBalanceCurrency?: string;
	    availableBalanceError?: string;
	    availableBalanceBasis?: string;
	    accountBalance?: string;
	    equity?: string;
	    usedMargin?: string;
	    marginRequired?: string;
	    marginRequiredCurrency?: string;
	    marginError?: string;
	    marginBasis?: string;
	    liquidationPrice?: string;
	    liquidationError?: string;
	    liquidationBasis?: string;
	    liquidationKind?: string;
	    metricsTime?: number;
	    feedKey: domain.FeedKey;
	    side: string;
	    quantity: string;
	    leverage: number;
	    entryPrice: string;

	    static createFrom(source: any = {}) {
	        return new Metrics(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.availableBalance = source["availableBalance"];
	        this.availableBalanceCurrency = source["availableBalanceCurrency"];
	        this.availableBalanceError = source["availableBalanceError"];
	        this.availableBalanceBasis = source["availableBalanceBasis"];
	        this.accountBalance = source["accountBalance"];
	        this.equity = source["equity"];
	        this.usedMargin = source["usedMargin"];
	        this.marginRequired = source["marginRequired"];
	        this.marginRequiredCurrency = source["marginRequiredCurrency"];
	        this.marginError = source["marginError"];
	        this.marginBasis = source["marginBasis"];
	        this.liquidationPrice = source["liquidationPrice"];
	        this.liquidationError = source["liquidationError"];
	        this.liquidationBasis = source["liquidationBasis"];
	        this.liquidationKind = source["liquidationKind"];
	        this.metricsTime = source["metricsTime"];
	        this.feedKey = this.convertValues(source["feedKey"], domain.FeedKey);
	        this.side = source["side"];
	        this.quantity = source["quantity"];
	        this.leverage = source["leverage"];
	        this.entryPrice = source["entryPrice"];
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

export namespace main {

	export class StartupTask {
	    id: string;
	    message: string;
	    state: string;
	    elapsedMs: number;
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new StartupTask(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.message = source["message"];
	        this.state = source["state"];
	        this.elapsedMs = source["elapsedMs"];
	        this.error = source["error"];
	    }
	}
	export class StartupStatus {
	    workspaceReady: boolean;
	    ready: boolean;
	    phase: string;
	    elapsedMs: number;
	    tasks: StartupTask[];
	    error?: string;

	    static createFrom(source: any = {}) {
	        return new StartupStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspaceReady = source["workspaceReady"];
	        this.ready = source["ready"];
	        this.phase = source["phase"];
	        this.elapsedMs = source["elapsedMs"];
	        this.tasks = this.convertValues(source["tasks"], StartupTask);
	        this.error = source["error"];
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
