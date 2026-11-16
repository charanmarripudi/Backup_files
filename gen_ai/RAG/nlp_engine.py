"""
NLP ENGINE - spaCy-powered entity extraction for VTS SQL Generator
Uses a blank model + EntityRuler (no internet required, ~5ms per query)
Replaces 15+ scattered regex calls with a single fast pipeline pass.
"""

import re
import spacy
import functools
import logging
from spacy.language import Language
from typing import Dict, List, Optional, Any

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Pre-compiled fast regex (module-level, compiled once)
# ---------------------------------------------------------------------------
_RE_VEHICLE_ID   = re.compile(r'\b([A-Z]{2}\d{1,2}[A-Z]{1,2}\d{4})\b', re.IGNORECASE)
_RE_NUMBER       = re.compile(r'\b(\d+(?:\.\d+)?)\b')
_RE_INTERVAL     = re.compile(r'\b(?:in\s+the\s+)?(last|past|previous)\s+(\d+)\s+(day|week|month|year)s?\b', re.IGNORECASE)
_RE_WITHIN       = re.compile(r'within\s+(\d+)\s+(hour|minute|day)s?', re.IGNORECASE)
_RE_DATE         = re.compile(r'\b(\d{4}-\d{2}-\d{2})\b')
_RE_DATE_DMY     = re.compile(r'\b(\d{2})-(\d{2})-(\d{4})\b')
_RE_OPERATOR     = re.compile(r'(greater|more|higher|above|over|less|lower|below|under)\s*(?:than)?\s*([\d.]+)', re.IGNORECASE)
_RE_LIMIT        = re.compile(r'\b(top|bottom|highest|lowest|most|worst|best|least)\s*(\d+)?', re.IGNORECASE)
_RE_CAPACITY     = re.compile(r'capacity\s*(?:is)?\s*(greater|less|more|under|over|above|below|<|>)\s*(?:than)?\s*(\d+)', re.IGNORECASE)
_RE_ZONE         = re.compile(r"['\"]?(\w+)['\"]?\s+zone", re.IGNORECASE)
_RE_REGION       = re.compile(r"['\"]?(\w+)['\"]?\s+region", re.IGNORECASE)
_RE_SEQUENCE     = re.compile(r'followed by|then|subsequently|later|after', re.IGNORECASE)
_RE_NEGATION_KW  = re.compile(r'\b(not\s+generated|no\s+alerts?|no\s+violations?|without|never\s+had|no\s+data|zero trips?|0 trips?)\b', re.IGNORECASE)
_RE_CLEAN_SQL_MD = re.compile(r'```sql\n?|```', re.IGNORECASE)
_RE_CLEAN_PREFIX = re.compile(r'^(here is the sql|sure|here is the query|the sql query is|sql|query)[:\s]*', re.IGNORECASE)
_RE_SQL_START    = re.compile(r'(^|\n)\s*(SELECT|WITH)\s+', re.IGNORECASE)
_RE_BLACKLIST    = re.compile(r'\bblacklisted\b(?!\s*=\s*\')', re.IGNORECASE)
_RE_IS_BLACKLIST = re.compile(r'\bis_blacklisted\b', re.IGNORECASE)
_RE_VIOLATION_COUNT = re.compile(r'\b(\w+\.)?violation_count\b')
_RE_ENGINE_NUM   = re.compile(r'\bengine_number\b')
_RE_CHASSIS_NUM  = re.compile(r'\bchassis_number\b')
_RE_BLOCKED_COL  = re.compile(r'\bblocked\s*(=|!=|IS|IN)\b', re.IGNORECASE)
_RE_MONTH_YEAR   = re.compile(r'\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d{4})\b', re.IGNORECASE)

TOTAL_VIOLATIONS_EXPR = (
    "(COALESCE(stoppage_violations_count,0) + COALESCE(route_deviation_count,0) + "
    "COALESCE(speed_violation_count,0) + COALESCE(night_driving_count,0) + "
    "COALESCE(device_offline_count,0) + COALESCE(device_tamper_count,0) + "
    "COALESCE(continuous_driving_count,0) + COALESCE(no_halt_zone_count,0) + "
    "COALESCE(main_supply_removal_count,0))"
)

# ---------------------------------------------------------------------------
# Domain EntityRuler patterns
# ---------------------------------------------------------------------------
_VIOLATION_TYPES = [
    "power disconnect", "main supply removal", "device offline", "device tamper",
    "speed violation", "stoppage violation", "route deviation", "night driving",
    "continuous driving", "no halt", "harsh acceleration", "harsh braking",
    "over-speeding", "emlock", "swipe out failure",
]

_ZONES = ["north", "south", "east", "west", "central", "northeast", "northwest",
          "southeast", "southwest"]

_TIME_WORDS = ["today", "yesterday", "last month", "last week", "last year",
               "this month", "this week", "this year", "last quarter"]

_SEVERITY = ["critical", "high", "medium", "low"]

_ALERT_STATUS = ["open", "close", "closed", "in progress"]

_RISK_QUALIFIERS = ["high risk", "low risk", "risky", "safe", "critical risk"]


def _build_patterns() -> List[dict]:
    patterns = []
    for v in _VIOLATION_TYPES:
        patterns.append({"label": "VIOLATION_TYPE", "pattern": v})
    for z in _ZONES:
        patterns.append({"label": "ZONE", "pattern": z + " zone"})
        patterns.append({"label": "ZONE", "pattern": z})
    for tw in _TIME_WORDS:
        patterns.append({"label": "TIME_EXPR", "pattern": tw})
    for s in _SEVERITY:
        patterns.append({"label": "SEVERITY", "pattern": s + " severity"})
        patterns.append({"label": "SEVERITY", "pattern": s})
    for a in _ALERT_STATUS:
        patterns.append({"label": "ALERT_STATUS", "pattern": a + " alerts"})
        patterns.append({"label": "ALERT_STATUS", "pattern": a + " alert"})
    for r in _RISK_QUALIFIERS:
        patterns.append({"label": "RISK_QUALIFIER", "pattern": r})
    return patterns


# ---------------------------------------------------------------------------
# Singleton NLP engine (loaded once, reused across all requests)
# ---------------------------------------------------------------------------
class VTSNLPEngine:
    _instance: Optional["VTSNLPEngine"] = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
            cls._instance._initialized = False
        return cls._instance

    def __init__(self):
        if self._initialized:
            return
        logger.info("[NLPEngine] Initializing spaCy blank model + EntityRuler...")
        self.nlp = spacy.blank("en")
        ruler = self.nlp.add_pipe("entity_ruler", config={"overwrite_ents": True})
        ruler.add_patterns(_build_patterns())
        self._initialized = True
        logger.info("[NLPEngine] Ready.")

    @functools.lru_cache(maxsize=512)
    def parse(self, question: str) -> Dict[str, Any]:
        """
        Fast single-pass extraction of all entities from a question.
        Result is LRU-cached so repeated calls are free (dict lookup).
        Returns a rich dict used by sql_generator and rag_system.
        """
        q_lower = question.lower()
        doc = self.nlp(q_lower)

        result: Dict[str, Any] = {
            # spaCy EntityRuler results
            "violation_types": [],
            "zones":           [],
            "time_exprs":      [],
            "severity":        [],
            "alert_status":    [],
            "risk_qualifier":  None,

            # Regex-extracted entities
            "vehicle_id":      None,
            "numbers":         [],
            "interval":        None,        # e.g. "INTERVAL '3 months'"
            "within_interval": None,        # e.g. "INTERVAL '1 hour'"
            "specific_date":   None,
            "operator":        None,        # ">", "<"
            "threshold":       None,        # numeric value for operator
            "limit":           10,
            "order_dir":       "DESC",
            "zone_name":       None,
            "region_name":     None,
            "capacity_op":     None,
            "capacity_val":    None,

            # Semantic flags
            "has_negation":    False,
            "has_sequence":    False,
            "is_transporter":  False,
            "is_vehicle":      False,
            "is_driver":       False,
            "is_zone":         False,
            "is_region":       False,
            "has_time_filter": False,
        }

        # --- spaCy entity ruler results ---
        for ent in doc.ents:
            lbl = ent.label_
            if lbl == "VIOLATION_TYPE":
                result["violation_types"].append(ent.text)
            elif lbl == "ZONE":
                result["zones"].append(ent.text)
                result["is_zone"] = True
            elif lbl == "TIME_EXPR":
                result["time_exprs"].append(ent.text)
                result["has_time_filter"] = True
            elif lbl == "SEVERITY":
                result["severity"].append(ent.text)
            elif lbl == "ALERT_STATUS":
                result["alert_status"].append(ent.text)
            elif lbl == "RISK_QUALIFIER":
                result["risk_qualifier"] = ent.text

        # --- Vehicle ID ---
        vm = _RE_VEHICLE_ID.search(question)
        if vm:
            result["vehicle_id"] = vm.group(1).upper()

        # --- Time interval ("last 3 months" or "last month/week/year") ---
        im = _RE_INTERVAL.search(q_lower)
        if im:
            result["interval"] = f"INTERVAL '{im.group(2)} {im.group(3)}s'"
            result["has_time_filter"] = True
        else:
            # Handle "last month / last week / last year / last quarter" (no explicit number)
            _word_intervals = {
                "last month": "INTERVAL '1 month'",
                "this month": "INTERVAL '1 month'",
                "last week":  "INTERVAL '1 week'",
                "this week":  "INTERVAL '1 week'",
                "last year":  "INTERVAL '1 year'",
                "this year":  "INTERVAL '1 year'",
                "last quarter": "INTERVAL '3 months'",
                "last 7 days":  "INTERVAL '7 days'",
                "last 30 days": "INTERVAL '30 days'",
            }
            for phrase, interval in _word_intervals.items():
                if phrase in q_lower:
                    result["interval"] = interval
                    result["has_time_filter"] = True
                    break

        # --- Within interval ("within 1 hour") ---
        wm = _RE_WITHIN.search(q_lower)
        if wm:
            result["within_interval"] = f"INTERVAL '{wm.group(1)} {wm.group(2)}'"

        # --- Specific date ---
        dm = _RE_DATE.search(q_lower)
        if dm:
            result["specific_date"] = dm.group(1)
            result["has_time_filter"] = True
        else:
            dm2 = _RE_DATE_DMY.search(q_lower)
            if dm2:
                result["specific_date"] = f"{dm2.group(3)}-{dm2.group(2)}-{dm2.group(1)}"
                result["has_time_filter"] = True

        # --- Comparison operator + threshold ---
        om = _RE_OPERATOR.search(q_lower)
        if om:
            word = om.group(1).lower()
            result["operator"] = "<" if word in ("less", "lower", "below", "under") else ">"
            result["threshold"] = om.group(2)
            result["order_dir"] = "ASC" if result["operator"] == "<" else "DESC"

        # --- Ranking limit ---
        lm = _RE_LIMIT.search(q_lower)
        if lm:
            word = lm.group(1).lower()
            val  = lm.group(2)
            result["limit"] = int(val) if val else (1 if word in ("highest", "lowest", "most", "worst", "best", "least") else 10)
            result["order_dir"] = "ASC" if word in ("bottom", "lowest", "least") else "DESC"

        # --- Capacity condition ---
        cm = _RE_CAPACITY.search(q_lower)
        if cm:
            op_word = cm.group(1).lower()
            result["capacity_op"]  = "<" if op_word in ("less", "under", "below", "<") else ">"
            result["capacity_val"] = cm.group(2)

        # --- Zone / region ---
        zm = _RE_ZONE.search(q_lower)
        if zm:
            result["zone_name"] = zm.group(1)
            result["is_zone"] = True
        rm = _RE_REGION.search(q_lower)
        if rm:
            result["region_name"] = rm.group(1)
            result["is_region"] = True

        # --- Semantic flags ---
        result["has_negation"]   = bool(_RE_NEGATION_KW.search(q_lower))
        result["has_sequence"]   = bool(_RE_SEQUENCE.search(q_lower))
        result["is_transporter"] = "transporter" in q_lower
        result["is_vehicle"]     = any(w in q_lower for w in ("vehicle", "truck", "fleet"))
        result["is_driver"]      = "driver" in q_lower

        # --- All raw numbers ---
        result["numbers"] = _RE_NUMBER.findall(q_lower)

        return result


# ---------------------------------------------------------------------------
# Intent classifier (fast, domain-tuned, cacheable)
# ---------------------------------------------------------------------------
_TREND_KW    = frozenset(["trend", "over time", "month-wise", "weekly", "daily",
                           "history of", "timeline", "progression", "fluctuation"])
_COMPARE_KW  = frozenset(["compare", "vs", "difference", "versus", "growth", "change"])
_COUNT_KW    = frozenset(["how many", "count of", "number of", "count "])
_RANK_KW     = frozenset(["top", "highest", "lowest", "best", "worst", "most", "rank"])
_DIST_KW     = frozenset(["breakdown", "split", "distribution", "group by", "per "])
_SUMM_KW     = frozenset(["average", "total", "sum", "max", "min", "overall"])
_EXIST_KW    = frozenset(["is there", "exist", "have", "any"])
_AGG_FILTER  = re.compile(
    r'(more than|at least|greater than|fewer than|less than)\s+\d+\s+'
    r'(?:different\s+)?(zones|regions|trips|violations|alerts|vehicles|drivers)', re.I)
_PART_RANK   = re.compile(r'\b(each|per|every|group by|breakdown)\b', re.I)
_LEAST_WORD  = re.compile(r'\b(?<!at\s)least\b', re.I)

@functools.lru_cache(maxsize=512)
def classify_intent(q_lower: str) -> str:
    """Fast cached intent classification."""
    if any(k in q_lower for k in _TREND_KW):
        return "TREND"
    if any(k in q_lower for k in _COMPARE_KW):
        return "COMPARISON"
    if any(k in q_lower for k in _COUNT_KW):
        return "COUNT"
    if any(k in q_lower for k in _RANK_KW) or _LEAST_WORD.search(q_lower):
        return "PARTITIONED_RANKING" if _PART_RANK.search(q_lower) else "RANKING"
    if _AGG_FILTER.search(q_lower):
        return "AGGREGATION_FILTER"
    if any(k in q_lower for k in _DIST_KW):
        return "DISTRIBUTION"
    if " by " in q_lower and not any(
        p in q_lower for p in ("followed by", "order by", "sort by", "filter by", "driven by", "operated by")
    ):
        return "DISTRIBUTION"
    if any(k in q_lower for k in _SUMM_KW):
        return "SUMMARY"
    if any(k in q_lower for k in _EXIST_KW):
        return "EXISTENCE"
    return "LIST"


# Module-level singleton
nlp_engine = VTSNLPEngine()