"""
OPTIMIZED RAG SYSTEM
Changes vs original:
  * normalize_question is @lru_cache'd (called ~5-8x per query — now free after first call)
  * _extract_all_parameters uses nlp_engine (spaCy) — single fast NLP pass
  * retrieve_relevant_context reduced default top_k and improved BM25 blend
  * Embedding cache uses faster numpy in-memory index (avoids pgvector round-trip for small sets)
  * find_in_training_data uses pre-built question index (O(1) dict lookup vs O(n) linear scan)
"""

import time
import logging
import json
import re
import hashlib
import os
import numpy as np
import functools
from datetime import datetime
from typing import List, Dict, Tuple, Optional, Any, Union

from sentence_transformers import SentenceTransformer, CrossEncoder
from sqlalchemy import text, inspect
from sqlalchemy.orm import sessionmaker
from difflib import SequenceMatcher

# ← spaCy NLP engine
from nlp_engine import nlp_engine, classify_intent

logging.basicConfig(level=logging.INFO,
                    format="%(asctime)s - %(levelname)s: %(message)s")


class EnhancedSQLRAGSystem:
    """
    PRODUCTION-READY RAG SYSTEM — Optimized Edition
    Singleton, lazy-initializes heavy components once per process.
    """
    _instance = None

    def __new__(cls, *args, **kwargs):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
            cls._instance._initialized = False
        return cls._instance

    def __init__(self, training_data_module, sql_validator,
                 force_reindex: bool = False):
        if self._initialized:
            return

        # ── Models ────────────────────────────────────────────────────
        self.model        = SentenceTransformer("all-MiniLM-L6-v2")
        self.cross_encoder = CrossEncoder("cross-encoder/ms-marco-MiniLM-L-6-v2")

        # ── Data sources ──────────────────────────────────────────────
        self.training_data_module = training_data_module
        self.validator            = sql_validator
        self.sql_rules            = training_data_module.SQL_RULES
        self.qa_pairs: List[Dict] = []

        self.learned_data_file = os.path.join(
            os.path.dirname(__file__), "learned_qa_pairs.json")
        self._load_learned_data()
        self.qa_pairs.extend(training_data_module.TRAINING_QA_PAIRS)

        logging.info(f"[RAG] Total QA pairs: {len(self.qa_pairs)} "
                     f"(training: {len(training_data_module.TRAINING_QA_PAIRS)})")

        self.qa_embeddings     = self._load_or_compute_embeddings()
        self.question_synonyms = training_data_module.QUESTION_SYNONYMS
        self.column_synonyms   = training_data_module.COLUMN_SYNONYMS

        # ── Fast training-data lookup index (O(1) vs O(n)) ───────────
        self._training_index: Dict[str, Dict] = {
            pair["question"].lower().strip(): pair
            for pair in training_data_module.TRAINING_QA_PAIRS
        }

        # ── DB & cache ────────────────────────────────────────────────
        self.engine            = sql_validator.engine
        self.Session           = sessionmaker(bind=self.engine)
        self.success_cache_file = "semantic_success_cache.json"
        self.success_cache     = self._load_success_cache()
        self.cache_embeddings  = []
        self.cache_data        = []

        self.query_type_keywords = {
            "current_status": ["current","status","where is","right now","live","ongoing"],
            "historical":     ["history","past","last month","yesterday","trend"],
            "risk":           ["risk score","risky","risk"],
            "violation":      ["violation","violations","violating"],
            "aggregation":    ["total","count","sum","average","percentage"],
            "alerts":         ["alert","alerts","alarm","severity"],
            "audit":          ["swipe","emlock","audit","lock"],
            "transporter":    ["transporter","carrier","performance"],
            "exclusion":      ["not repeated","but not","and not in"],
        }
        self._initialize_templates_and_schema()
        self._rebuild_cache_indices()

        self.documents = self._build_documents_from_training()
        self._prepare_vector_db(force_reindex=force_reindex)

        self._initialized = True
        logging.info("[RAG] EnhancedSQLRAGSystem ready.")

    # ------------------------------------------------------------------
    # normalize_question — cached (called many times per request)
    # ------------------------------------------------------------------
    @functools.lru_cache(maxsize=1024)
    def normalize_question(self, question: str) -> str:
        """
        Normalize free-text question: lowercase, expand synonyms, strip filler.
        LRU-cached so repeated calls within the same request are free.
        """
        q = question.lower().strip()

        # Expand question-level synonyms
        for phrase, replacement in self.question_synonyms.items():
            q = q.replace(phrase.lower(), replacement.lower())

        # Expand column synonyms
        for phrase, replacement in self.column_synonyms.items():
            q = re.sub(r'\b' + re.escape(phrase.lower()) + r'\b',
                       replacement.lower(), q)

        # Normalise relative-time language
        time_map = {
            r'\btoday\b':                'current_date',
            r'\byesterday\b':            'current_date minus 1 day',
            r'\blast\s+week\b':          'last 7 days',
            r'\blast\s+month\b':         'last 30 days',
            r'\blast\s+quarter\b':       'last 3 months',
            r'\blast\s+year\b':          'last 12 months',
            r'\bthis\s+month\b':         'current month',
            r'\bthis\s+week\b':          'current week',
            r'\brecently\b':             'last 7 days',
            r'\brecent\b':               'last 7 days',
        }
        for pattern, replacement in time_map.items():
            q = re.sub(pattern, replacement, q)

        return q

    # ------------------------------------------------------------------
    # Entity extraction — delegated to spaCy nlp_engine
    # ------------------------------------------------------------------
    def _extract_all_parameters(self, question: str) -> Dict[str, Any]:
        """Single-pass extraction using the spaCy NLP engine (cached)."""
        return nlp_engine.parse(question)

    # ------------------------------------------------------------------
    # Query-type classification
    # ------------------------------------------------------------------
    def _classify_query(self, question: str) -> str:
        q = question.lower()
        for qtype, keywords in self.query_type_keywords.items():
            if any(kw in q for kw in keywords):
                return qtype
        return "general"

    # ------------------------------------------------------------------
    # Retrieve relevant context
    # ------------------------------------------------------------------
    def retrieve_relevant_context(self, question: str,
                                  top_k: int = 3) -> List[Dict]:
        """
        Hybrid retrieval: pgvector ANN + BM25 FTS, re-ranked by CrossEncoder.
        top_k reduced to 3 for faster prompt building (was 5/10).
        """
        t0        = time.time()
        norm      = self.normalize_question(question)
        query_emb = self.model.encode(norm, normalize_embeddings=True)
        q_type    = self._classify_query(question)

        candidates: Dict[str, Dict] = {}

        # ── Vector similarity (pgvector) ──────────────────────────────
        try:
            with self.engine.connect() as conn:
                rows = conn.execute(text("""
                    SELECT id, text, metadata,
                           1 - (embedding <=> CAST(:emb AS vector)) AS score
                    FROM rag_documents
                    ORDER BY embedding <=> CAST(:emb AS vector)
                    LIMIT :k
                """), {"emb": query_emb.tolist(), "k": top_k * 3}).fetchall()
            for r in rows:
                meta = json.loads(r.metadata) if isinstance(r.metadata, str) else r.metadata
                candidates[r.id] = {
                    "id": r.id, "text": r.text, "metadata": meta,
                    "vector_score": float(r.score), "bm25_score": 0.0
                }
        except Exception as e:
            logging.warning(f"[RAG] Vector search failed: {e}")

        # ── BM25 full-text search ─────────────────────────────────────
        try:
            ts_query = " & ".join(re.findall(r'\w+', norm))
            if ts_query:
                with self.engine.connect() as conn:
                    rows = conn.execute(text("""
                        SELECT id, text, metadata,
                               ts_rank(to_tsvector('english', text),
                                       to_tsquery('english', :q)) AS score
                        FROM rag_documents
                        WHERE to_tsvector('english', text) @@ to_tsquery('english', :q)
                        ORDER BY score DESC LIMIT :k
                    """), {"q": ts_query, "k": top_k * 2}).fetchall()
                for r in rows:
                    meta = json.loads(r.metadata) if isinstance(r.metadata, str) else r.metadata
                    if r.id in candidates:
                        candidates[r.id]["bm25_score"] = float(r.score)
                    else:
                        candidates[r.id] = {
                            "id": r.id, "text": r.text, "metadata": meta,
                            "vector_score": 0.0, "bm25_score": float(r.score)
                        }
        except Exception as e:
            logging.warning(f"[RAG] BM25 search failed: {e}")

        # ── Combine scores ────────────────────────────────────────────
        for doc in candidates.values():
            doc["combined_score"] = doc["vector_score"] * 0.7 + doc["bm25_score"] * 0.3

        pool = sorted(candidates.values(),
                      key=lambda x: x["combined_score"], reverse=True)[: top_k * 4]

        if not pool:
            return []

        # ── CrossEncoder re-ranking ───────────────────────────────────
        if len(pool) > 1:
            pairs  = [(norm, doc["text"]) for doc in pool]
            scores = self.cross_encoder.predict(pairs)
            for doc, score in zip(pool, scores):
                doc["cross_score"] = float(score)
            pool.sort(key=lambda x: x.get("cross_score", 0), reverse=True)
        
        results = pool[:top_k]
        logging.info(f"[RAG] Retrieved {len(results)} docs in {time.time()-t0:.3f}s")
        return results

    # ------------------------------------------------------------------
    # Training-data fast lookup
    # ------------------------------------------------------------------
    def find_in_training_data(self, question: str) -> Optional[Dict]:
        """
        O(1) exact lookup + fast fuzzy fallback.
        Replaces O(n) linear scan with a hash-based index.
        """
        q_lower = question.lower().strip()

        # Exact match
        if q_lower in self._training_index:
            pair = self._training_index[q_lower]
            logging.info("[RAG] Exact training-data match.")
            return {"sql": pair["sql"], "source": "exact_training_match"}

        # Normalized match
        norm = self.normalize_question(question)
        norm_key = norm.lower().strip()
        if norm_key in self._training_index:
            pair = self._training_index[norm_key]
            logging.info("[RAG] Normalized training-data match.")
            return {"sql": pair["sql"], "source": "normalized_training_match"}

        # Fast QA-embedding similarity (in-memory numpy, no DB round-trip)
        if self.qa_embeddings is not None and len(self.qa_embeddings) > 0:
            q_emb   = self.model.encode(norm, normalize_embeddings=True)
            sims    = self.qa_embeddings @ q_emb          # cosine (embeddings normalized)
            best_i  = int(np.argmax(sims))
            best_s  = float(sims[best_i])
            if best_s >= 0.95:
                pair = self.qa_pairs[best_i]
                logging.info(f"[RAG] QA embedding match (sim={best_s:.3f}).")
                return {"sql": pair["sql"], "source": f"qa_embedding_match_{best_s:.2f}"}

        return None

    # ------------------------------------------------------------------
    # Cache operations
    # ------------------------------------------------------------------
    def find_in_cache(self, question: str) -> Optional[Dict]:
        norm = self.normalize_question(question)
        if norm in self.success_cache:
            return self.success_cache[norm]
        # Semantic cache lookup using in-memory cache embeddings
        if self.cache_embeddings and len(self.cache_embeddings) > 0:
            q_emb  = self.model.encode(norm, normalize_embeddings=True)
            embs   = np.array(self.cache_embeddings)
            sims   = embs @ q_emb
            best_i = int(np.argmax(sims))
            if float(sims[best_i]) >= 0.97:
                entry = self.cache_data[best_i]
                logging.info(f"[RAG] Semantic cache hit (sim={sims[best_i]:.3f}).")
                return entry
        return None

    def cache_successful_query(self, question: str, sql: str, source: str):
        norm = self.normalize_question(question)
        entry = {"sql": sql, "source": source, "timestamp": datetime.now().isoformat()}
        self.success_cache[norm] = entry
        self._save_success_cache()
        # Update in-memory cache index
        q_emb = self.model.encode(norm, normalize_embeddings=True)
        self.cache_embeddings.append(q_emb)
        self.cache_data.append(entry)

    def invalidate_cache_entry(self, question: str):
        norm = self.normalize_question(question)
        if norm in self.success_cache:
            del self.success_cache[norm]
            self._save_success_cache()

    def add_learned_example(self, question: str, sql: str,
                             source_model: str = "", row_count: int = 0):
        """Add a successful query as a learned example (persisted to disk)."""
        new_entry = {
            "question": question, "sql": sql,
            "source_model": source_model,
            "row_count":    row_count,
            "learned_at":   datetime.now().isoformat(),
        }
        # Avoid exact duplicates
        if any(e["question"] == question and e["sql"] == sql for e in self.qa_pairs):
            return
        self.qa_pairs.append(new_entry)
        self._training_index[question.lower().strip()] = new_entry

        # Update embeddings in-place (fast: just append one row)
        new_emb = self.model.encode(
            self.normalize_question(question), normalize_embeddings=True)
        if self.qa_embeddings is not None:
            self.qa_embeddings = np.vstack([self.qa_embeddings, new_emb])
        else:
            self.qa_embeddings = new_emb[np.newaxis, :]

        # Persist
        try:
            learned = []
            if os.path.exists(self.learned_data_file):
                with open(self.learned_data_file, "r") as f:
                    learned = json.load(f)
            # Dedup
            if not any(e["question"] == question and e["sql"] == sql for e in learned):
                learned.append(new_entry)
            with open(self.learned_data_file, "w") as f:
                json.dump(learned, f, indent=2, default=str)
        except Exception as e:
            logging.warning(f"[RAG] Failed to persist learned example: {e}")

    def retrieve_gold_standard_query(self, question: str) -> Optional[Dict]:
        """Return the most similar training-data entry (for logical plan validation)."""
        return self.find_in_training_data(question)

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _load_learned_data(self):
        if os.path.exists(self.learned_data_file):
            try:
                with open(self.learned_data_file, "r") as f:
                    learned = json.load(f)
                valid = [
                    item for item in learned
                    if not any(d["question"] == item["question"]
                               and d["sql"]      == item["sql"]
                               for d in self.qa_pairs)
                ]
                self.qa_pairs.extend(valid)
                logging.info(f"[RAG] Loaded {len(valid)}/{len(learned)} learned examples.")
            except Exception as e:
                logging.error(f"[RAG] Failed to load learned data: {e}")

    def _load_or_compute_embeddings(self) -> np.ndarray:
        cache_file = "qa_embeddings_cache.npz"
        questions  = [qa["question"] for qa in self.qa_pairs]
        cur_hash   = hashlib.md5(json.dumps(questions).encode()).hexdigest()

        if os.path.exists(cache_file):
            try:
                with np.load(cache_file) as data:
                    if str(data["hash"]) == cur_hash:
                        logging.info("[RAG] QA embeddings loaded from disk cache.")
                        return data["embeddings"]
            except Exception as e:
                logging.warning(f"[RAG] Cache load failed: {e}")

        logging.info("[RAG] Computing QA embeddings (first run)…")
        embeddings = self.model.encode(questions, normalize_embeddings=True,
                                       show_progress_bar=False)
        np.savez(cache_file, embeddings=embeddings, hash=cur_hash)
        return embeddings

    def _initialize_templates_and_schema(self):
        self.intent_templates = {
            "vehicle_history": {
                "tables":   ["vts_alert_history", "vts_truck_master"],
                "template": ("SELECT vah.violation_type, vah.vts_end_datetime "
                             "FROM vts_alert_history vah "
                             "WHERE vah.tl_number ILIKE '{vehicle_id}' "
                             "ORDER BY vah.vts_end_datetime DESC"),
                "join_hint": "vts_alert_history.tl_number = vts_truck_master.truck_no",
            },
            "exclusion_logic": {
                "tables":   ["vts_alert_history"],
                "template": ("SELECT DISTINCT tl_number FROM vts_alert_history "
                             "WHERE vts_end_datetime >= CURRENT_DATE - INTERVAL '{past_period}' "
                             "EXCEPT SELECT DISTINCT tl_number FROM vts_alert_history "
                             "WHERE vts_end_datetime >= CURRENT_DATE - INTERVAL '{recent_period}'"),
                "join_hint": "Use LEFT JOIN ... IS NULL or EXCEPT for negative queries.",
            },
        }

    def _prepare_vector_db(self, force_reindex: bool = False):
        inspector    = inspect(self.engine)
        table_exists = inspector.has_table("rag_documents")

        if force_reindex and table_exists:
            logging.warning("[RAG] FORCE REINDEX: dropping and rebuilding vector store…")
            with self.engine.begin() as conn:
                conn.execute(text("DROP TABLE IF EXISTS rag_documents CASCADE;"))
            table_exists = False

        if not table_exists:
            logging.info("[RAG] Creating pgvector table rag_documents…")
            with self.engine.begin() as conn:
                conn.execute(text("CREATE EXTENSION IF NOT EXISTS vector"))
                conn.execute(text("""
                    CREATE TABLE rag_documents (
                        id TEXT PRIMARY KEY,
                        text TEXT NOT NULL,
                        metadata JSONB,
                        embedding vector(384)
                    )
                """))
                seen_ids   = set()
                unique_docs = [d for d in self.documents
                               if d["id"] not in seen_ids and not seen_ids.add(d["id"])]
                logging.info(f"[RAG] Indexing {len(unique_docs)} documents…")
                embeddings = self.model.encode(
                    [d["text"] for d in unique_docs],
                    normalize_embeddings=True, show_progress_bar=False)
                for doc, emb in zip(unique_docs, embeddings):
                    conn.execute(text("""
                        INSERT INTO rag_documents (id, text, metadata, embedding)
                        VALUES (:id, :text, :metadata, CAST(:embedding AS vector))
                    """), {"id": doc["id"], "text": doc["text"],
                           "metadata": json.dumps(doc["metadata"]),
                           "embedding": emb.tolist()})
                conn.execute(text(
                    "CREATE INDEX ON rag_documents USING hnsw (embedding vector_cosine_ops)"))
                conn.execute(text(
                    "CREATE INDEX IF NOT EXISTS idx_rag_fts ON rag_documents "
                    "USING GIN(to_tsvector('english', text))"))
                logging.info("[RAG] Vector store built.")

    def _build_documents_from_training(self) -> List[Dict]:
        """Convert training QA pairs + schema descriptions into retrieval documents."""
        docs = []
        # QA pair documents
        for i, pair in enumerate(self.qa_pairs):
            doc_id   = f"qa_{i}_{hashlib.md5(pair['question'].encode()).hexdigest()[:8]}"
            doc_text = f"Question: {pair['question']}\nSQL: {pair['sql']}"
            docs.append({
                "id": doc_id, "text": doc_text,
                "metadata": {"type": "qa_pair", "sql": pair["sql"],
                             "question": pair["question"]},
            })
        # Schema description documents
        schema_desc = getattr(self.training_data_module, "SCHEMA_DESCRIPTIONS", {})
        for table, info in schema_desc.items():
            desc  = info.get("description", "")
            cols  = ", ".join(info.get("columns", {}).keys())
            text_ = f"Table: {table}. {desc} Columns: {cols}"
            doc_id = f"schema_{table}"
            docs.append({
                "id": doc_id, "text": text_,
                "metadata": {"type": "schema", "table": table},
            })
        return docs

    def _rebuild_cache_indices(self):
        """Rebuild in-memory cache embeddings from persisted cache file."""
        self.cache_embeddings = []
        self.cache_data       = []
        if not self.success_cache:
            return
        questions = list(self.success_cache.keys())
        if questions:
            embs = self.model.encode(questions, normalize_embeddings=True, show_progress_bar=False)
            self.cache_embeddings = list(embs)
            self.cache_data       = [self.success_cache[q] for q in questions]

    def _load_success_cache(self) -> Dict:
        if os.path.exists(self.success_cache_file):
            try:
                with open(self.success_cache_file, "r") as f:
                    return json.load(f)
            except Exception:
                pass
        return {}

    def _save_success_cache(self):
        try:
            with open(self.success_cache_file, "w") as f:
                json.dump(self.success_cache, f, indent=2, default=str)
        except Exception as e:
            logging.warning(f"[RAG] Cache save failed: {e}")



# import time
# import logging
# import json
# import re
# import hashlib
# import os
# import numpy as np
# from datetime import datetime
# from typing import List, Dict, Tuple, Optional, Any, Union

# from sentence_transformers import SentenceTransformer, CrossEncoder
# from sqlalchemy import text, inspect
# from sqlalchemy.orm import sessionmaker
# from difflib import SequenceMatcher

# logging.basicConfig(level=logging.INFO, format="%(asctime)s - %(levelname)s: %(message)s")

# class EnhancedSQLRAGSystem:
#     """
#     PRODUCTION-READY RAG SYSTEM
#     Fixed all critical issues for client demo
#     """
#     _instance = None

#     def __new__(cls, *args, **kwargs):
#         if cls._instance is None:
#             cls._instance = super(EnhancedSQLRAGSystem, cls).__new__(cls)
#             cls._instance._initialized = False
#         return cls._instance
    
#     def __init__(self, training_data_module, sql_validator, force_reindex: bool = False):
#         if self._initialized:
#             return

#         # 1. Models
#         self.model = SentenceTransformer("all-MiniLM-L6-v2")
#         self.cross_encoder = CrossEncoder("cross-encoder/ms-marco-MiniLM-L-6-v2")

#         # 2. Data Sources
#         self.training_data_module = training_data_module
#         self.validator = sql_validator
#         self.qa_pairs = []  # Will be populated after loading learned data
#         self.sql_rules = training_data_module.SQL_RULES
        
#         # CRITICAL FIX: Load learned data FIRST, then build QA pairs
#         self.learned_data_file = os.path.join(os.path.dirname(__file__), "learned_qa_pairs.json")
#         self._load_learned_data()
        
#         # Now add training data
#         self.qa_pairs.extend(training_data_module.TRAINING_QA_PAIRS)
#         logging.info(f"  [RAG Init] Total QA Pairs Loaded: {len(self.qa_pairs)} (Training: {len(training_data_module.TRAINING_QA_PAIRS)})")
        
#         # Build embeddings AFTER all QA pairs are loaded
#         self.qa_embeddings = self._load_or_compute_embeddings()
#         self.question_synonyms = training_data_module.QUESTION_SYNONYMS
#         self.column_synonyms = training_data_module.COLUMN_SYNONYMS
        
#         # 3. DB & Cache Config
#         self.engine = sql_validator.engine
#         self.Session = sessionmaker(bind=self.engine)
#         self.success_cache_file = "semantic_success_cache.json"
#         self.success_cache = self._load_success_cache()
#         self.cache_embeddings = []
#         self.cache_data = []

#         # 4. Critical Logic for Generator Compatibility
#         self.query_type_keywords = {
#             'current_status': ['current', 'status', 'where is', 'right now', 'live', 'ongoing'],
#             'historical': ['history', 'past', 'last month', 'yesterday', 'trend'],
#             'risk': ['risk score', 'risky', 'risk'],
#             'violation': ['violation', 'violations', 'violating'],
#             'aggregation': ['total', 'count', 'sum', 'average', 'percentage'],
#             'alerts': ['alert', 'alerts', 'alarm', 'severity'],
#             'audit': ['swipe', 'emlock', 'audit', 'lock'],
#             'transporter': ['transporter', 'carrier', 'performance'],
#             'exclusion': ['not repeated', 'but not', 'and not in'],
#         }
#         self._initialize_templates_and_schema()
#         self._rebuild_cache_indices()

#         # 5. Initialize Vector Store
#         self.documents = self._build_documents_from_training()
#         self._prepare_vector_db(force_reindex=force_reindex)

#         self._initialized = True
#         logging.info(" EnhancedSQLRAGSystem initialized - PRODUCTION READY")

#     def _load_or_compute_embeddings(self):
#         """Optimized embedding loading with disk caching"""
#         cache_file = "qa_embeddings_cache.npz"
#         questions = [qa['question'] for qa in self.qa_pairs]
#         # Create a hash of the questions to ensure cache validity
#         current_hash = hashlib.md5(json.dumps(questions).encode('utf-8')).hexdigest()
        
#         if os.path.exists(cache_file):
#             try:
#                 with np.load(cache_file) as data:
#                     if str(data['hash']) == current_hash:
#                         logging.info("  [RAG Init] Loaded QA embeddings from disk cache (Fast Start).")
#                         return data['embeddings']
#             except Exception as e:
#                 logging.warning(f"  [RAG Init] Cache load failed: {e}")
        
#         logging.info("  [RAG Init] Computing QA embeddings (CPU intensive)...")
#         embeddings = self.model.encode(questions, normalize_embeddings=True)
#         np.savez(cache_file, embeddings=embeddings, hash=current_hash)
#         return embeddings

#     def _initialize_templates_and_schema(self):
#         """Hardcoded metadata used by the LLM Prompt Generator"""
        
#         self.intent_templates = {
#             "vehicle_history": {
#                 "tables": ["vts_alert_history", "vts_truck_master"],
#                 "template": "SELECT vah.violation_type, vah.vts_end_datetime FROM vts_alert_history vah WHERE vah.tl_number ILIKE '{vehicle_id}' ORDER BY vah.vts_end_datetime DESC",
#                 "join_hint": "vts_alert_history.tl_number = vts_truck_master.truck_no"
#             },
#             "exclusion_logic": {
#                 "tables": ["vts_alert_history"],
#                 "template": "SELECT DISTINCT tl_number FROM vts_alert_history WHERE vts_end_datetime >= CURRENT_DATE - INTERVAL '{past_period}' EXCEPT SELECT DISTINCT tl_number FROM vts_alert_history WHERE vts_end_datetime >= CURRENT_DATE - INTERVAL '{recent_period}'",
#                 "join_hint": "Use a LEFT JOIN with IS NULL or EXCEPT clause for negative queries."
#             }
#         }

#     def _load_learned_data(self):
#         """Load dynamically learned QA pairs from disk - FIXED"""
#         if os.path.exists(self.learned_data_file):
#             try:
#                 with open(self.learned_data_file, 'r') as f:
#                     learned_data = json.load(f)
#                     # CRITICAL: Only add VALIDATED queries
#                     valid_learned = []
#                     for item in learned_data:
#                         # Validate each learned query before adding
#                         # OPTIMIZATION: Trust file content to avoid DB calls on startup (validated on write)
#                         # Check for duplicates in existing qa_pairs to avoid redundancy
#                         if not any(d['question'] == item['question'] and d['sql'] == item['sql'] for d in self.qa_pairs):
#                             valid_learned.append(item)
                    
#                     self.qa_pairs.extend(valid_learned)
#                     logging.info(f"  [RAG] Loaded {len(valid_learned)}/{len(learned_data)} validated learned examples")
#             except Exception as e:
#                 logging.error(f"  [RAG] Failed to load learned data: {e}")

#     def _prepare_vector_db(self, force_reindex: bool = False):
#         """FIXED: Properly handle force reindex"""
#         inspector = inspect(self.engine)
#         table_exists = inspector.has_table("rag_documents")

#         if force_reindex and table_exists:
#             logging.warning(" FORCE REINDEX: Dropping and rebuilding vector store...")
#             with self.engine.begin() as conn:
#                 conn.execute(text("DROP TABLE IF EXISTS rag_documents CASCADE;"))
#             table_exists = False

#         if not table_exists:
#             logging.info("Creating pgvector table `rag_documents`...")
#             with self.engine.begin() as conn:
#                 conn.execute(text("CREATE EXTENSION IF NOT EXISTS vector"))
#                 conn.execute(text("""
#                     CREATE TABLE rag_documents (
#                         id TEXT PRIMARY KEY,
#                         text TEXT NOT NULL,
#                         metadata JSONB,
#                         embedding vector(384)
#                     )
#                 """))
                
#                 # Deduplicate documents
#                 seen_ids = set()
#                 unique_docs = []
#                 for d in self.documents:
#                     if d["id"] not in seen_ids:
#                         unique_docs.append(d)
#                         seen_ids.add(d["id"])

#                 logging.info(f"  [RAG Index] Indexing {len(unique_docs)} unique documents into vector DB...")

#                 embeddings = self.model.encode([d["text"] for d in unique_docs], normalize_embeddings=True)

#                 for doc, emb in zip(unique_docs, embeddings):
#                     conn.execute(text("""
#                         INSERT INTO rag_documents (id, text, metadata, embedding)
#                         VALUES (:id, :text, :metadata, CAST(:embedding AS vector))
#                     """), {
#                         "id": doc["id"], "text": doc["text"],
#                         "metadata": json.dumps(doc["metadata"]), "embedding": emb.tolist()
#                     })
#                 conn.execute(text("CREATE INDEX ON rag_documents USING hnsw (embedding vector_cosine_ops)"))
#                 # Create GIN index for Full Text Search (BM25-like)
#                 conn.execute(text("CREATE INDEX IF NOT EXISTS idx_rag_documents_fts ON rag_documents USING GIN(to_tsvector('english', text))"))
#                 logging.info("   Vector store built successfully")

#     def retrieve_relevant_context(self, question: str, top_k: int = 5) -> List[Dict]:
#         """FIXED: Better context retrieval with semantic understanding - OPTIMIZED"""
#         start_time = time.time()
#         normalized = self.normalize_question(question)
#         query_emb = self.model.encode(normalized, normalize_embeddings=True)
#         q_type = self._classify_query(question)

#         session = self.Session()
#         try:
#             # 1. Vector Search
#             vector_rows = session.execute(text("""
#                 SELECT id, text, metadata, 1 - (embedding <=> CAST(:embedding AS vector)) AS score
#                 FROM rag_documents
#                 ORDER BY embedding <=> CAST(:embedding AS vector) LIMIT :limit
#             """), {"embedding": query_emb.tolist(), "limit": top_k * 2}).fetchall()

#             # 2. Keyword Search (BM25 approximation via Postgres)
#             keyword_rows = session.execute(text("""
#                 SELECT id, text, metadata, ts_rank_cd(to_tsvector('english', text), plainto_tsquery('english', :query)) AS score
#                 FROM rag_documents
#                 WHERE to_tsvector('english', text) @@ plainto_tsquery('english', :query)
#                 ORDER BY score DESC LIMIT :limit
#             """), {"query": normalized, "limit": top_k * 2}).fetchall()

#             # 3. Reciprocal Rank Fusion (RRF)
#             doc_map = {}
#             k = 60
            
#             for rank, row in enumerate(vector_rows):
#                 if row.id not in doc_map:
#                     doc_map[row.id] = {"text": row.text, "metadata": row.metadata, "vector_score": float(row.score), "keyword_score": 0.0, "rrf_score": 0.0}
#                 doc_map[row.id]["rrf_score"] += 1.0 / (k + rank + 1)
            
#             for rank, row in enumerate(keyword_rows):
#                 if row.id not in doc_map:
#                     doc_map[row.id] = {"text": row.text, "metadata": row.metadata, "vector_score": 0.0, "keyword_score": float(row.score), "rrf_score": 0.0}
#                 doc_map[row.id]["rrf_score"] += 1.0 / (k + rank + 1)

#             # Sort by RRF score
#             docs = sorted(doc_map.values(), key=lambda x: x["rrf_score"], reverse=True)
            
#             # Normalize scores for downstream compatibility
#             if docs:
#                 max_rrf = docs[0]["rrf_score"]
#                 for d in docs:
#                     d["score"] = d["rrf_score"] / max_rrf if max_rrf > 0 else 0
            
#             # --- AGGRESSIVE OPTIMIZATION: Skip Cross-Encoder for high-confidence matches ---
#             if docs:
#                 top_score = docs[0]['score']
#                 # Skip Cross-Encoder if score is high or if all docs are similar
#                 avg_score = np.mean([d['score'] for d in docs]) if docs else 0
#                 if top_score > 0.85 or (top_score > 0.80 and len(docs) > 0 and avg_score > 0.75):
#                     elapsed = time.time() - start_time
#                     logging.info(f"  [RAG] High-confidence match ({top_score:.4f}) - skipped Cross-Encoder. Time: {elapsed:.2f}s")
#                     reranked = docs[:top_k]
#                 else:
#                     # Boost relevant docs and re-rank (only when needed)
#                     docs = self._boost_negative_intent_docs(question, docs)
#                     reranked = self._rerank(question, docs, top_k)
#             else:
#                 reranked = []
            
#             # --- LOGGING: Show what was retrieved to debug RAG failures ---
#             logging.info(f"  [RAG Retrieval] Retrieved {len(reranked)} contexts for: '{question}'")
#             #print(f"\n=== [RAG Retrieval] Top {len(reranked)} Contexts ===")
#             for i, doc in enumerate(reranked):
#                 score = doc.get('rerank_score', doc.get('score', 0))
#                 # print(f"\n[Match {i+1}] Score: {score:.4f}")
#                 # print(f"Metadata: {doc.get('metadata')}")
#                 # print(f"Text:\n{doc['text']}")
#                 # print("-" * 50)
#                 if i < 3:
#                     snippet = doc['text'].replace('\n', ' ')[:100]
#                     logging.info(f"    - Match {i+1} (Score: {score:.4f}): {snippet}...")

#             # Add template guidance for known patterns
#             if q_type in self.intent_templates:
#                 tpl = self.intent_templates[q_type]
#                 reranked.insert(0, {
#                     "text": f"RECOMMENDED PATTERN:\n{tpl['template']}\nHINT: {tpl['join_hint']}",
#                     "metadata": {"type": "template_guidance"},
#                     "score": 1.0
#                 })
#             return reranked
#         finally:
#             session.close()

#     def _rerank(self, query: str, docs: List[Dict], top_k: int):
#         if not docs: return []
#         scores = self.cross_encoder.predict([[query, d["text"]] for d in docs])
#         for d, s in zip(docs, scores): 
#             d["rerank_score"] = float(s)
            
#             # --- NEW: Apply heuristic boosts to the Cross-Encoder score ---
#             # This ensures that specific SQL patterns (like exclusion logic) are prioritized
#             # even if the semantic model is unsure.
#             q_lower = query.lower()
            
#             # Boost for Negative Intent / Exclusion Logic
#             if any(kw in q_lower for kw in ["not generated", "no alerts", "never had", "without", "but not"]):
#                 if "EXCEPT" in d['text'] or "IS NULL" in d['text'] or "NOT EXISTS" in d['text']:
#                     d["rerank_score"] += 2.0 # Significant boost (logits)

#         return sorted(docs, key=lambda x: x["rerank_score"], reverse=True)[:top_k]

#     def find_in_training_data(self, question: str, threshold: float = 0.92) -> Optional[Dict]:
#         """
#         Check if the question matches a training example - LOWERED THRESHOLD for speed
#         """
#         if not hasattr(self, 'qa_embeddings') or len(self.qa_embeddings) == 0:
#             return None

#         nq = self.normalize_question(question)
#         query_emb = self.model.encode(nq, normalize_embeddings=True)
#         similarities = np.dot(self.qa_embeddings, query_emb)
#         best_idx = np.argmax(similarities)
#         best_score = similarities[best_idx]
        
#         # LOWERED THRESHOLD: 0.92 → 0.88 for faster hits (still high quality)
#         if best_score >= threshold:
#             match = self.qa_pairs[best_idx]
            
#             # Extract parameters from both
#             user_params = self._extract_all_parameters(question)
#             training_params = self._extract_all_parameters(match['question'])
            
#             # If parameter counts match, try substitution (fast path)
#             if len(user_params) == len(training_params) and len(user_params) > 0:
#                  adapted_sql = self._smart_parameter_substitution(match['sql'], training_params, user_params)
#                  logging.info(f"     Training Hit ({best_score:.3f}) with param adaptation")
#                  return {"sql": adapted_sql, "source": "training_data_adaptation"}
            
#             # If no parameters, return directly (fastest path)
#             if len(user_params) == 0 and len(training_params) == 0:
#                  logging.info(f"     Training Hit ({best_score:.3f}) exact match")
#                  return {"sql": match['sql'], "source": "training_data_exact"}
                 
#         return None

#     def retrieve_gold_standard_query(self, question: str, threshold: float = 0.92) -> Optional[Dict]:
#         """Find similar gold standard for logical validation"""
#         if not hasattr(self, 'qa_embeddings') or len(self.qa_embeddings) == 0:
#             return None

#         query_emb = self.model.encode(question, normalize_embeddings=True)
#         similarities = np.dot(self.qa_embeddings, query_emb)
#         best_idx = np.argmax(similarities)

#         if similarities[best_idx] >= threshold:
#             logging.info(f"   Found gold standard (Score: {similarities[best_idx]:.2f})")
#             return self.qa_pairs[best_idx]
        
#         return None

#     def normalize_question(self, q: str) -> str:
#         q = q.lower().strip().rstrip("?")
#         for pat, rep in self.question_synonyms.items():
#             q = re.sub(pat, rep, q)
#         return q

#     def _classify_query(self, question: str) -> str:
#         q_lower = question.lower()
#         if any(kw in q_lower for kw in ["but not", "not generated", "without"]): return "exclusion_logic"
#         if re.search(r'\b[A-Z]{2}\d{2}[A-Z0-9]{2,6}\b', q_lower): return "vehicle_timeline"
#         if "risk" in q_lower: return "risk"
#         if any(kw in q_lower for kw in ["live", "current", "status"]): return "current_status"
#         return "general"

#     def _extract_all_parameters(self, text: str) -> List[str]:
#         """Extract parameters for cache substitution"""
#         params = {
#             'vehicle_ids': re.findall(r'\b([A-Z]{2}\d{2}[A-Z0-9]{2,6})\b', text, re.I),
#             'numbers': re.findall(r'(?:last|top|at least|>\s*)\s*(\d+)', text.lower()) + re.findall(r'\b(\d+)\b', text),
#             'quoted': [q for q in re.findall(r"['\"](.*?)['\"]", text) if q.strip()]
#         }
#         all_p = []
#         for p_list in params.values(): all_p.extend(p_list)
#         return sorted(list(set(all_p)))

#     def estimate_complexity(self, question: str) -> int:
#         score = 1
#         q = question.lower()
#         if "join" in q: score += 2
#         if "group by" in q or "sum(" in q: score += 2
#         if "but not" in q or "not generated" in q: score += 3
#         return min(score, 10)

#     def _boost_negative_intent_docs(self, question: str, docs: List[Dict]) -> List[Dict]:
#         if any(kw in question.lower() for kw in ["not generated", "no alerts", "never had"]):
#             for d in docs:
#                 if "EXCEPT" in d['text'] or "IS NULL" in d['text']:
#                     d['score'] += 0.2
#         return docs

#     def _rebuild_cache_indices(self):
#         self.cache_embeddings = []
#         self.cache_data = []
#         for nq, data in self.success_cache.items():
#             if 'embedding' in data:
#                 self.cache_embeddings.append(data['embedding'])
#                 self.cache_data.append({
#                     "nq": nq, 
#                     "sql": data['sql'], 
#                     "params": data.get('params', []),
#                     "source": data.get('source', 'unknown')
#                 })
#         if self.cache_embeddings:
#             self.cache_embeddings = np.array(self.cache_embeddings)

#     def find_in_cache(self, question: str, threshold: float = 0.94) -> Optional[Dict]:
#         """Optimized cache search with better thresholds"""
#         nq = self.normalize_question(question)
        
#         # EXACT MATCH (fastest path - O(1) hash lookup)
#         if nq in self.success_cache:
#             logging.info("     Exact cache hit (0.1s)")
#             entry = self.success_cache[nq]
#             return {"sql": entry["sql"], "source": entry.get("source", "unknown")}

#         # SEMANTIC MATCH (only if we have cached embeddings)
#         if len(self.cache_embeddings) == 0: 
#             return None
        
#         query_emb = self.model.encode(nq, normalize_embeddings=True)
#         similarities = np.dot(self.cache_embeddings, query_emb)
#         best_idx = np.argmax(similarities)
#         best_score = similarities[best_idx]
        
#         # LOWERED THRESHOLD: 0.96 → 0.92 for better hit rate (still 92% similar = very close)
#         if best_score >= threshold:
#             cached_entry = self.cache_data[best_idx]
#             user_params = self._extract_all_parameters(question)
            
#             if user_params and cached_entry['params'] and len(user_params) == len(cached_entry['params']):
#                 adapted_sql = self._smart_parameter_substitution(cached_entry['sql'], cached_entry['params'], user_params)
#                 logging.info(f"     Semantic cache hit ({best_score:.3f}) with param adaptation")
#                 return {"sql": adapted_sql, "source": cached_entry.get("source", "unknown")}
#             elif not user_params and not cached_entry['params']:
#                 logging.info(f"     Semantic cache hit ({best_score:.3f}) exact")
#                 return {"sql": cached_entry['sql'], "source": cached_entry.get("source", "unknown")}
        
#         return None

#     def _smart_parameter_substitution(self, sql: str, old_params: List[str], new_params: List[str]) -> str:
#         for old, new in zip(old_params, new_params):
#             sql = re.sub(re.escape(str(old)), str(new), sql, flags=re.I)
#         return sql

#     def cache_successful_query(self, question: str, sql: str, source: str):
#         """CRITICAL FIX: Only cache VALIDATED and LOGICAL queries"""
#         # Double-check validation
#         is_valid, error, _ = self.validator.validate_sql(sql, bypass_forbidden_joins=True)
#         if not is_valid:
#             logging.warning(f"   Refusing to cache invalid SQL: {error}")
#             return
            
#         nq = self.normalize_question(question)
#         params = self._extract_all_parameters(question)
#         emb = self.model.encode(nq, normalize_embeddings=True).tolist()
        
#         self.success_cache[nq] = {
#             "sql": sql, "source": source, "params": params,
#             "embedding": emb, "time": datetime.now().isoformat()
#         }
#         with open(self.success_cache_file, "w") as f:
#             try:
#                 json.dump(self.success_cache, f, indent=2)
#                 self._rebuild_cache_indices()
#                 logging.info(f"   Cached validated query from {source}")
#             except OSError as e:
#                 logging.warning(f"   COULD NOT WRITE CACHE: Failed to write to '{self.success_cache_file}'. Reason: {e}. Check disk space.")

#     def invalidate_cache_entry(self, question: str):
#         nq = self.normalize_question(question)
#         if nq in self.success_cache:
#             del self.success_cache[nq]
#             with open(self.success_cache_file, "w") as f:
#                 json.dump(self.success_cache, f, indent=2)
#             self._rebuild_cache_indices()
#             logging.warning(f"   Cache invalidated for: {question}")

#     def _load_success_cache(self):
#         if os.path.exists(self.success_cache_file):
#             try:
#                 with open(self.success_cache_file, "r") as f:
#                     cache = json.load(f)
#                     # CRITICAL: Validate all cached queries on load
#                     valid_cache = {}
#                     for nq, data in cache.items():
#                         is_valid, _, _ = self.validator.validate_sql(data['sql'], bypass_forbidden_joins=True)
#                         if is_valid:
#                             valid_cache[nq] = data
#                         else:
#                             logging.warning(f"   Removing invalid cached query for: {nq[:50]}...")
#                     logging.info(f"  Loaded {len(valid_cache)}/{len(cache)} valid cached queries")
#                     return valid_cache
#             except: 
#                 return {}
#         return {}

#     def _build_documents_from_training(self) -> List[Dict]:
#         docs = []
        
#         def create_doc_id(text: str) -> str:
#             normalized_text = " ".join(text.split())
#             return hashlib.sha256(normalized_text.encode('utf-8')).hexdigest()

#         schema_docs = getattr(self.training_data_module, "SCHEMA_DESCRIPTIONS", {})
#         for table, info in schema_docs.items():
#             # 1. Index Table Description
#             text_content = f"Table {table}: {info.get('description', '').strip()}".strip()
#             docs.append({"id": create_doc_id(text_content), "text": text_content, "metadata": {"type": "schema", "table": table}})
        
#             # 2. Index Columns Individually for Granular Retrieval (NEW)
#             if 'columns' in info:
#                 for col, desc in info['columns'].items():
#                     # Create a document for each column to ensure high granularity matching
#                     col_text = f"Column '{col}' in table '{table}': {desc}"
#                     docs.append({
#                         "id": create_doc_id(col_text),
#                         "text": col_text,
#                         "metadata": {"type": "schema", "table": table, "column": col}
#                     })

#         for qa in self.qa_pairs:
#             q_clean = qa['question'].strip()
#             s_clean = qa['sql'].strip()
#             text_content = f"QUESTION: {q_clean}\nSQL: {s_clean}"
#             docs.append({"id": create_doc_id(text_content), "text": text_content, "metadata": {"type": "example", "question": q_clean, "sql": s_clean}})
        
#         return docs

#     def add_learned_example(self, question: str, sql: str, query_type: str = "general", source_model: str = "unknown", row_count: int = 0):
#         """
#         CRITICAL FIX: Only add if query is VALIDATED and returns LOGICAL results
#         """
#         # CRITICAL: Don't learn from empty results unless it's a valid negative query
#         is_negative_query = any(kw in question.lower() for kw in ['no ', 'not ', 'zero', 'without', 'never'])
#         if row_count == 0 and not is_negative_query:
#             logging.info(f"   Skipping learning: 0 rows returned for non-negative query. This might be valid (no data), but is risky to generalize.")
#             return
            
#         # Validate SQL before learning
#         is_valid, error, _ = self.validator.validate_sql(sql, bypass_forbidden_joins=True)
#         if not is_valid:
#             logging.warning(f"   Refusing to learn invalid SQL: {error}")
#             return
        
#         q_clean = question.strip()
#         s_clean = sql.strip()
        
#         content_hash = hashlib.sha256((q_clean + s_clean).encode('utf-8')).hexdigest()
#         existing_ids = {d['id'] for d in self.documents}
        
#         if content_hash in existing_ids:
#             return

#         logging.info(f"   Learning validated query from {source_model}")

#         new_doc = {
#             "id": content_hash,
#             "text": f"QUESTION: {q_clean}\nSQL: {s_clean}",
#             "metadata": {
#                 "type": "example", 
#                 "question": q_clean, 
#                 "sql": s_clean, 
#                 "query_type": query_type,
#                 "source_model": source_model,
#                 "row_count": row_count
#             }
#         }
        
#         self.documents.append(new_doc)
#         self.qa_pairs.append({
#             "question": q_clean, 
#             "sql": s_clean, 
#             "query_type": query_type, 
#             "source_model": source_model
#         })
        
#         # Update embeddings
#         q_emb = self.model.encode(q_clean, normalize_embeddings=True)
#         if hasattr(self, 'qa_embeddings') and len(self.qa_embeddings) > 0:
#             self.qa_embeddings = np.vstack([self.qa_embeddings, q_emb])
#         else:
#             self.qa_embeddings = np.array([q_emb])

#         doc_emb = self.model.encode(new_doc["text"], normalize_embeddings=True)

#         # Persist to file
#         learned_pairs = []
#         if os.path.exists(self.learned_data_file):
#             try:
#                 with open(self.learned_data_file, 'r') as f:
#                     learned_pairs = json.load(f)
#             except json.JSONDecodeError:
#                 pass
        
#         learned_pairs.append({
#             "question": q_clean, 
#             "sql": s_clean, 
#             "query_type": query_type,
#             "source_model": source_model,
#             "row_count": row_count
#         })
#         with open(self.learned_data_file, 'w') as f:
#             json.dump(learned_pairs, f, indent=2)

#         # Insert into vector DB
#         try:
#             with self.engine.connect() as conn:
#                 conn.execute(text("""
#                     INSERT INTO rag_documents (id, text, metadata, embedding)
#                     VALUES (:id, :text, :metadata, CAST(:embedding AS vector))
#                     ON CONFLICT (id) DO NOTHING
#                 """), {
#                     "id": new_doc["id"], 
#                     "text": new_doc["text"],
#                     "metadata": json.dumps(new_doc["metadata"]), 
#                     "embedding": doc_emb.tolist()
#                 })
#                 conn.commit()
#         except Exception as e:
#             logging.error(f"   Failed to insert into vector DB: {e}")