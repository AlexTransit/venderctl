-- DROP SCHEMA public;

CREATE SCHEMA public AUTHORIZATION postgres;

COMMENT ON SCHEMA public IS 'standard public schema';

-- DROP TYPE public.cashless_state;

CREATE TYPE public.cashless_state AS ENUM (
);

-- DROP TYPE public.cashless_state1;

CREATE TYPE public.cashless_state1 AS ENUM (
);

-- DROP TYPE public.ghstore;

CREATE TYPE public.ghstore (
	INPUT = ghstore_in,
	OUTPUT = ghstore_out,
	ALIGNMENT = 4,
	STORAGE = plain,
	CATEGORY = U,
	DELIMITER = ',');

-- DROP TYPE public.hstore;

CREATE TYPE public.hstore (
	INPUT = hstore_in,
	OUTPUT = hstore_out,
	RECEIVE = hstore_recv,
	SEND = hstore_send,
	ALIGNMENT = 4,
	STORAGE = any,
	CATEGORY = U,
	DELIMITER = ',');

-- DROP TYPE public.tax_job_state;

CREATE TYPE public.tax_job_state AS ENUM (
);

-- DROP SEQUENCE public.pgmfavorites_seq_id;

CREATE SEQUENCE public.pgmfavorites_seq_id
	INCREMENT BY 1
	MINVALUE 1
	MAXVALUE 9223372036854775807
	START 1
	CACHE 1
	NO CYCLE;
-- DROP SEQUENCE public.tax_job_id_seq;

CREATE SEQUENCE public.tax_job_id_seq
	INCREMENT BY 1
	MINVALUE 1
	MAXVALUE 9223372036854775807
	START 1
	CACHE 1
	NO CYCLE;
-- DROP SEQUENCE public.web_admin_messages_id_seq;

CREATE SEQUENCE public.web_admin_messages_id_seq
	INCREMENT BY 1
	MINVALUE 1
	MAXVALUE 9223372036854775807
	START 1
	CACHE 1
	NO CYCLE;-- public.cashless definition

-- Drop table

-- DROP TABLE public.cashless;

CREATE TABLE public.cashless (
	order_state int4 DEFAULT 0 NOT NULL,
	vmid int4 NOT NULL,
	create_date timestamptz DEFAULT now() NOT NULL,
	credit_date timestamptz NULL,
	finish_date timestamptz NULL,
	payment_id varchar(20) NULL,
	paymentid int8 DEFAULT 0 NOT NULL,
	order_id varchar NOT NULL,
	amount int4 NOT NULL,
	credited int4 DEFAULT 0 NOT NULL,
	bank_commission int4 DEFAULT 0 NOT NULL,
	terminal text NULL,
	payer text NULL,
	terminal_id int4 DEFAULT 0 NOT NULL,
	bank_order_state int4 NULL,
	CONSTRAINT cashless_idx1 PRIMARY KEY (order_id),
	CONSTRAINT cashless_paymentid_key UNIQUE (paymentid)
);
CREATE UNIQUE INDEX cashless_idx ON public.cashless USING btree (payment_id, order_id);
CREATE UNIQUE INDEX cashless_vmid_payment_id_order_id_key ON public.cashless USING btree (vmid, payment_id, order_id);


-- public."catalog" definition

-- Drop table

-- DROP TABLE public."catalog";

CREATE TABLE public."catalog" (
	vmid int4 NOT NULL,
	code text NOT NULL,
	"name" text NOT NULL
);
CREATE UNIQUE INDEX idx_catalog_vmid_code_name ON public.catalog USING btree (name, code, vmid);


-- public.error definition

-- Drop table

-- DROP TABLE public.error;

CREATE TABLE public.error (
	vmid int4 NOT NULL,
	vmtime timestamptz NULL,
	received timestamptz NOT NULL,
	code int4 NULL,
	message text NOT NULL,
	count int4 NULL,
	checked bool DEFAULT false NOT NULL,
	app_version text NULL
);
CREATE INDEX idx_error_vmid_vmtime_code ON public.error USING btree (vmid, vmtime DESC, code);


-- public.ingest definition

-- Drop table

-- DROP TABLE public.ingest;

CREATE TABLE public.ingest (
	received timestamptz DEFAULT CURRENT_TIMESTAMP NOT NULL,
	vmid int4 NOT NULL,
	done bool DEFAULT false NOT NULL,
	raw bytea NOT NULL
);
CREATE INDEX idx_ingest_received ON public.ingest USING btree (received) WHERE (NOT done);


-- public.inventory definition

-- Drop table

-- DROP TABLE public.inventory;

CREATE TABLE public.inventory (
	vmid int4 NULL,
	at_service bool NOT NULL,
	vmtime timestamptz NOT NULL,
	received timestamptz NOT NULL,
	inventory public.hstore NULL,
	cashbox_bill public.hstore NULL,
	cashbox_coin public.hstore NULL,
	change_bill public.hstore NULL,
	change_coin public.hstore NULL,
	CONSTRAINT inventory_vmid_key UNIQUE (vmid, at_service)
);


-- public.old_state definition

-- Drop table

-- DROP TABLE public.old_state;

CREATE TABLE public.old_state (
	state int4 NULL
);


-- public.pgmfavorites definition

-- Drop table

-- DROP TABLE public.pgmfavorites;

CREATE TABLE public.pgmfavorites (
	id int8 DEFAULT nextval('pgmfavorites_seq_id'::regclass) NOT NULL,
	fav_name text NULL,
	fav_source text NULL,
	fav_path text NULL,
	CONSTRAINT pgmfavorites_pkey PRIMARY KEY (id)
);


-- public.robot definition

-- Drop table

-- DROP TABLE public.robot;

CREATE TABLE public.robot (
	vmid int4 NOT NULL,
	vmnum int4 NOT NULL,
	description text NULL,
	"location" text NULL,
	bunkers public.hstore NULL,
	"mobile-number" text NULL,
	serial_num numeric(7) NOT NULL,
	work bool DEFAULT true NOT NULL,
	in_robo public.hstore NULL, -- inventoiry inside robo
	to_robo public.hstore NULL,
	notes public.hstore NULL,
	hardware public.hstore NULL,
	geolocation public.hstore NULL,
	service_time timestamptz NULL,
	last_sync timestamptz NULL,
	service_ingredient public.hstore NULL,
	last_sync_ingredient public.hstore NULL,
	money_inside public.hstore NULL,
	"show" bool NULL,
	CONSTRAINT "robot-key" PRIMARY KEY (vmid),
	CONSTRAINT "robot_mobile-number_key" UNIQUE ("mobile-number"),
	CONSTRAINT robot_serial_num_key UNIQUE (serial_num)
);

-- Column comments

COMMENT ON COLUMN public.robot.in_robo IS 'inventoiry inside robo';


-- public.state definition

-- Drop table

-- DROP TABLE public.state;

CREATE TABLE public.state (
	vmid int4 NOT NULL,
	state int4 NOT NULL,
	received timestamptz NOT NULL,
	connected bool NULL,
	contime timestamptz(0) NULL
);
CREATE UNIQUE INDEX idx_state_vmid_state_received ON public.state USING btree (vmid, state, received) WITH (fillfactor='10');


-- public.tax_job definition

-- Drop table

-- DROP TABLE public.tax_job;

CREATE TABLE public.tax_job (
	id bigserial NOT NULL,
	state public.tax_job_state NOT NULL,
	created timestamptz NOT NULL,
	modified timestamptz NOT NULL,
	scheduled timestamptz NULL,
	worker text NULL,
	processor text NULL,
	ext_id text NULL,
	"data" jsonb NULL,
	gross int4 NULL,
	notes _text NULL,
	ops jsonb NULL,
	CONSTRAINT tax_job_check CHECK ((NOT ((state = 'sched'::tax_job_state) AND (scheduled IS NULL)))),
	CONSTRAINT tax_job_check1 CHECK ((NOT ((state = 'busy'::tax_job_state) AND (worker IS NULL)))),
	CONSTRAINT tax_job_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_tax_job_help ON public.tax_job USING btree (modified) WHERE (state = 'help'::tax_job_state);
CREATE INDEX idx_tax_job_sched ON public.tax_job USING btree (scheduled, modified) WHERE (state = 'sched'::tax_job_state);

-- Table Triggers

create trigger tax_job_maint_after after
insert
    or
update
    on
    public.tax_job for each row execute procedure tax_job_maint_after();
create trigger tax_job_maint_before before
insert
    or
update
    on
    public.tax_job for each row execute procedure tax_job_maint_before();
create trigger tax_job_modified before
update
    on
    public.tax_job for each row
    when (((new.ext_id is distinct
from
    old.ext_id)
    or (new.data is distinct
from
    old.data)
    or (new.notes is distinct
from
    old.notes))) execute procedure tax_job_modified();


-- public.tg_chat definition

-- Drop table

-- DROP TABLE public.tg_chat;

CREATE TABLE public.tg_chat (
	create_date timestamp(0) DEFAULT now() NOT NULL,
	messageid int4 NOT NULL,
	fromid int8 NOT NULL,
	toid int8 NOT NULL,
	"date" int4 NOT NULL,
	"text" text NULL,
	changedate int4 NULL,
	changetext text NULL
);
CREATE UNIQUE INDEX tgchat_idx ON public.tg_chat USING btree (messageid, fromid, toid, date);
CREATE INDEX tgchat_idx1 ON public.tg_chat USING btree (fromid);
CREATE INDEX tgchat_idx2 ON public.tg_chat USING btree (toid);


-- public.tg_user definition

-- Drop table

-- DROP TABLE public.tg_user;

CREATE TABLE public.tg_user (
	"date" timestamp(0) DEFAULT now() NULL,
	ban bool DEFAULT false NULL,
	userid int8 NOT NULL,
	"name" text NULL,
	firstname text NULL,
	lastname text NULL,
	phonenumber text NULL,
	balance int4 NULL,
	credit int4 NULL,
	registerdate int4 NULL,
	diskont int4 DEFAULT 3 NULL,
	memo text NULL,
	defaultrobot int4 NULL,
	CONSTRAINT tg_user_pkey PRIMARY KEY (userid)
);


-- public.user_orders definition

-- Drop table

-- DROP TABLE public.user_orders;

CREATE TABLE public.user_orders (
	userid int8 NOT NULL,
	user_type int4 NOT NULL,
	"date" timestamptz DEFAULT now() NOT NULL,
	"action" text DEFAULT ''::text NOT NULL,
	balance_info numeric(8, 2) DEFAULT 0 NOT NULL
);
CREATE INDEX user_orders_idx ON public.user_orders USING btree (userid, user_type);
CREATE INDEX user_orders_idx1 ON public.user_orders USING btree (date);


-- public.user_sessions definition

-- Drop table

-- DROP TABLE public.user_sessions;

CREATE TABLE public.user_sessions (
	"token" text NOT NULL,
	userid int8 NOT NULL,
	user_type int4 DEFAULT 0 NULL,
	created_at timestamptz(0) DEFAULT now() NOT NULL,
	device_info text NULL,
	approved bool DEFAULT false NULL,
	revoked bool DEFAULT false NOT NULL,
	memo text NULL,
	endpoint text NULL,
	p256dh text NULL,
	auth text NULL,
	push_revoked bool DEFAULT false NOT NULL,
	CONSTRAINT user_sessions_pkey PRIMARY KEY (token)
);
CREATE INDEX user_sessions_userid_idx ON public.user_sessions USING btree (userid, user_type);


-- public.users definition

-- Drop table

-- DROP TABLE public.users;

CREATE TABLE public.users (
	userid int8 NOT NULL,
	user_type int4 DEFAULT 0 NOT NULL, -- user type. user type. look in OwnerType tele.proto
	ban bool DEFAULT false NOT NULL, -- blocking sign
	phone text DEFAULT ''::text NOT NULL, -- user phone number
	balance numeric(8, 2) DEFAULT 0 NOT NULL, -- user balance
	credit numeric(8, 2) DEFAULT 0 NOT NULL, -- user credit
	discount numeric(5, 2) DEFAULT 3 NOT NULL, -- user discount
	"name" text NULL,
	memo text NULL,
	defaultrobot int4 DEFAULT 0 NOT NULL, -- defaultrobot for client. 0 = auto
	"registerDate" timestamptz(0) DEFAULT now() NOT NULL,
	CONSTRAINT users_pkey PRIMARY KEY (userid, user_type)
);

-- Column comments

COMMENT ON COLUMN public.users.user_type IS 'user type. user type. look in OwnerType tele.proto';
COMMENT ON COLUMN public.users.ban IS 'blocking sign';
COMMENT ON COLUMN public.users.phone IS 'user phone number';
COMMENT ON COLUMN public.users.balance IS 'user balance';
COMMENT ON COLUMN public.users.credit IS 'user credit';
COMMENT ON COLUMN public.users.discount IS 'user discount';
COMMENT ON COLUMN public.users.defaultrobot IS 'defaultrobot for client. 0 = auto';


-- public.web_admin_messages definition

-- Drop table

-- DROP TABLE public.web_admin_messages;

CREATE TABLE public.web_admin_messages (
	id bigserial NOT NULL,
	userid int8 NOT NULL,
	user_type int4 NOT NULL,
	message text NOT NULL,
	reply text NULL,
	from_admin bool DEFAULT false NOT NULL,
	created_at timestamptz(0) DEFAULT now() NOT NULL,
	replied_at timestamptz(0) NULL,
	read_at timestamptz(0) NULL,
	CONSTRAINT web_admin_messages_pkey PRIMARY KEY (id)
);


-- public.web_auth_tokens definition

-- Drop table

-- DROP TABLE public.web_auth_tokens;

CREATE TABLE public.web_auth_tokens (
	"token" text NOT NULL,
	userid int8 NOT NULL,
	user_type int4 NOT NULL,
	created_at timestamp DEFAULT now() NULL,
	used bool DEFAULT false NULL,
	CONSTRAINT web_auth_tokens_pkey PRIMARY KEY (token)
);


-- public.trans definition

-- Drop table

-- DROP TABLE public.trans;

CREATE TABLE public.trans (
	vmid int4 NOT NULL,
	vmtime timestamptz NULL,
	received timestamptz NOT NULL,
	menu_code text NOT NULL,
	"options" _int4 NULL,
	price int4 NOT NULL,
	"method" int4 NOT NULL,
	tax_job_id int8 NULL,
	executer int8 NULL,
	executer_type int4 NULL,
	executer_str text NULL,
	CONSTRAINT trans_tax_job_id_fkey FOREIGN KEY (tax_job_id) REFERENCES public.tax_job(id) ON DELETE SET NULL ON UPDATE RESTRICT
);
CREATE UNIQUE INDEX idx_trans_vmid_vmtime ON public.trans USING btree (vmid, vmtime);
CREATE INDEX idx_trans_vmtime ON public.trans USING btree (vmtime);
CREATE INDEX trans_executer ON public.trans USING btree (executer);

-- Table Triggers

create trigger trans_tax after
insert
    on
    public.trans for each row execute procedure trans_tax_trigger();


-- public.tax_job_help source

CREATE OR REPLACE VIEW public.tax_job_help
AS SELECT tax_job.id,
    tax_job.state,
    tax_job.created,
    tax_job.modified,
    tax_job.scheduled,
    tax_job.worker,
    tax_job.processor,
    tax_job.ext_id,
    tax_job.data,
    tax_job.gross,
    tax_job.notes
   FROM tax_job
  WHERE tax_job.state = 'help'::tax_job_state
  ORDER BY tax_job.modified;



-- DROP FUNCTION public.akeys(hstore);

CREATE OR REPLACE FUNCTION public.akeys(hstore)
 RETURNS text[]
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_akeys$function$
;

-- DROP FUNCTION public.avals(hstore);

CREATE OR REPLACE FUNCTION public.avals(hstore)
 RETURNS text[]
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_avals$function$
;

-- DROP FUNCTION public.connect_update(int4, bool);

CREATE OR REPLACE FUNCTION public.connect_update(arg_vmid integer, arg_connect boolean)
 RETURNS integer
 LANGUAGE plpgsql
AS $function$
BEGIN
	INSERT INTO state (vmid, state, received, connected, contime)  VALUES (arg_vmid, 0, CURRENT_TIMESTAMP, arg_connect, CURRENT_TIMESTAMP)
    ON CONFLICT (vmid) DO UPDATE 
    SET connected = excluded.connected, contime = CURRENT_TIMESTAMP;
    return null;
END;
$function$
;

-- DROP FUNCTION public."defined"(hstore, text);

CREATE OR REPLACE FUNCTION public.defined(hstore, text)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_defined$function$
;

-- DROP FUNCTION public."delete"(hstore, hstore);

CREATE OR REPLACE FUNCTION public.delete(hstore, hstore)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_delete_hstore$function$
;

-- DROP FUNCTION public."delete"(hstore, text);

CREATE OR REPLACE FUNCTION public.delete(hstore, text)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_delete$function$
;

-- DROP FUNCTION public."delete"(hstore, _text);

CREATE OR REPLACE FUNCTION public.delete(hstore, text[])
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_delete_array$function$
;

-- DROP FUNCTION public."each"(in hstore, out text, out text);

CREATE OR REPLACE FUNCTION public.each(hs hstore, OUT key text, OUT value text)
 RETURNS SETOF record
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_each$function$
;

-- DROP FUNCTION public.exist(hstore, text);

CREATE OR REPLACE FUNCTION public.exist(hstore, text)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_exists$function$
;

-- DROP FUNCTION public.exists_all(hstore, _text);

CREATE OR REPLACE FUNCTION public.exists_all(hstore, text[])
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_exists_all$function$
;

-- DROP FUNCTION public.exists_any(hstore, _text);

CREATE OR REPLACE FUNCTION public.exists_any(hstore, text[])
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_exists_any$function$
;

-- DROP FUNCTION public.fetchval(hstore, text);

CREATE OR REPLACE FUNCTION public.fetchval(hstore, text)
 RETURNS text
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_fetchval$function$
;

-- DROP FUNCTION public.ghstore_compress(internal);

CREATE OR REPLACE FUNCTION public.ghstore_compress(internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_compress$function$
;

-- DROP FUNCTION public.ghstore_consistent(internal, hstore, int2, oid, internal);

CREATE OR REPLACE FUNCTION public.ghstore_consistent(internal, hstore, smallint, oid, internal)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_consistent$function$
;

-- DROP FUNCTION public.ghstore_decompress(internal);

CREATE OR REPLACE FUNCTION public.ghstore_decompress(internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_decompress$function$
;

-- DROP FUNCTION public.ghstore_in(cstring);

CREATE OR REPLACE FUNCTION public.ghstore_in(cstring)
 RETURNS ghstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_in$function$
;

-- DROP FUNCTION public.ghstore_out(ghstore);

CREATE OR REPLACE FUNCTION public.ghstore_out(ghstore)
 RETURNS cstring
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_out$function$
;

-- DROP FUNCTION public.ghstore_penalty(internal, internal, internal);

CREATE OR REPLACE FUNCTION public.ghstore_penalty(internal, internal, internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_penalty$function$
;

-- DROP FUNCTION public.ghstore_picksplit(internal, internal);

CREATE OR REPLACE FUNCTION public.ghstore_picksplit(internal, internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_picksplit$function$
;

-- DROP FUNCTION public.ghstore_same(ghstore, ghstore, internal);

CREATE OR REPLACE FUNCTION public.ghstore_same(ghstore, ghstore, internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_same$function$
;

-- DROP FUNCTION public.ghstore_union(internal, internal);

CREATE OR REPLACE FUNCTION public.ghstore_union(internal, internal)
 RETURNS ghstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$ghstore_union$function$
;

-- DROP FUNCTION public.gin_consistent_hstore(internal, int2, hstore, int4, internal, internal);

CREATE OR REPLACE FUNCTION public.gin_consistent_hstore(internal, smallint, hstore, integer, internal, internal)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$gin_consistent_hstore$function$
;

-- DROP FUNCTION public.gin_extract_hstore(hstore, internal);

CREATE OR REPLACE FUNCTION public.gin_extract_hstore(hstore, internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$gin_extract_hstore$function$
;

-- DROP FUNCTION public.gin_extract_hstore_query(hstore, internal, int2, internal, internal);

CREATE OR REPLACE FUNCTION public.gin_extract_hstore_query(hstore, internal, smallint, internal, internal)
 RETURNS internal
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$gin_extract_hstore_query$function$
;

-- DROP FUNCTION public.hs_concat(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hs_concat(hstore, hstore)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_concat$function$
;

-- DROP FUNCTION public.hs_contained(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hs_contained(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_contained$function$
;

-- DROP FUNCTION public.hs_contains(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hs_contains(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_contains$function$
;

-- DROP FUNCTION public.hstore(record);

CREATE OR REPLACE FUNCTION public.hstore(record)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE
AS '$libdir/hstore', $function$hstore_from_record$function$
;

-- DROP FUNCTION public.hstore(_text);

CREATE OR REPLACE FUNCTION public.hstore(text[])
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_from_array$function$
;

-- DROP FUNCTION public.hstore(text, text);

CREATE OR REPLACE FUNCTION public.hstore(text, text)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE
AS '$libdir/hstore', $function$hstore_from_text$function$
;

-- DROP FUNCTION public.hstore(_text, _text);

CREATE OR REPLACE FUNCTION public.hstore(text[], text[])
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE
AS '$libdir/hstore', $function$hstore_from_arrays$function$
;

-- DROP FUNCTION public.hstore_cmp(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_cmp(hstore, hstore)
 RETURNS integer
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_cmp$function$
;

-- DROP FUNCTION public.hstore_eq(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_eq(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_eq$function$
;

-- DROP FUNCTION public.hstore_ge(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_ge(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_ge$function$
;

-- DROP FUNCTION public.hstore_gt(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_gt(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_gt$function$
;

-- DROP FUNCTION public.hstore_hash(hstore);

CREATE OR REPLACE FUNCTION public.hstore_hash(hstore)
 RETURNS integer
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_hash$function$
;

-- DROP FUNCTION public.hstore_in(cstring);

CREATE OR REPLACE FUNCTION public.hstore_in(cstring)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_in$function$
;

-- DROP FUNCTION public.hstore_le(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_le(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_le$function$
;

-- DROP FUNCTION public.hstore_lt(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_lt(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_lt$function$
;

-- DROP FUNCTION public.hstore_ne(hstore, hstore);

CREATE OR REPLACE FUNCTION public.hstore_ne(hstore, hstore)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_ne$function$
;

-- DROP FUNCTION public.hstore_out(hstore);

CREATE OR REPLACE FUNCTION public.hstore_out(hstore)
 RETURNS cstring
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_out$function$
;

-- DROP FUNCTION public.hstore_recv(internal);

CREATE OR REPLACE FUNCTION public.hstore_recv(internal)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_recv$function$
;

-- DROP FUNCTION public.hstore_send(hstore);

CREATE OR REPLACE FUNCTION public.hstore_send(hstore)
 RETURNS bytea
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_send$function$
;

-- DROP FUNCTION public.hstore_to_array(hstore);

CREATE OR REPLACE FUNCTION public.hstore_to_array(hstore)
 RETURNS text[]
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_to_array$function$
;

-- DROP FUNCTION public.hstore_to_json(hstore);

CREATE OR REPLACE FUNCTION public.hstore_to_json(hstore)
 RETURNS json
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_to_json$function$
;

-- DROP FUNCTION public.hstore_to_json_loose(hstore);

CREATE OR REPLACE FUNCTION public.hstore_to_json_loose(hstore)
 RETURNS json
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_to_json_loose$function$
;

-- DROP FUNCTION public.hstore_to_jsonb(hstore);

CREATE OR REPLACE FUNCTION public.hstore_to_jsonb(hstore)
 RETURNS jsonb
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_to_jsonb$function$
;

-- DROP FUNCTION public.hstore_to_jsonb_loose(hstore);

CREATE OR REPLACE FUNCTION public.hstore_to_jsonb_loose(hstore)
 RETURNS jsonb
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_to_jsonb_loose$function$
;

-- DROP FUNCTION public.hstore_to_matrix(hstore);

CREATE OR REPLACE FUNCTION public.hstore_to_matrix(hstore)
 RETURNS text[]
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_to_matrix$function$
;

-- DROP FUNCTION public.hstore_version_diag(hstore);

CREATE OR REPLACE FUNCTION public.hstore_version_diag(hstore)
 RETURNS integer
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_version_diag$function$
;

-- DROP FUNCTION public.isdefined(hstore, text);

CREATE OR REPLACE FUNCTION public.isdefined(hstore, text)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_defined$function$
;

-- DROP FUNCTION public.isexists(hstore, text);

CREATE OR REPLACE FUNCTION public.isexists(hstore, text)
 RETURNS boolean
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_exists$function$
;

-- DROP FUNCTION public.populate_record(anyelement, hstore);

CREATE OR REPLACE FUNCTION public.populate_record(anyelement, hstore)
 RETURNS anyelement
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE
AS '$libdir/hstore', $function$hstore_populate_record$function$
;

-- DROP FUNCTION public.skeys(hstore);

CREATE OR REPLACE FUNCTION public.skeys(hstore)
 RETURNS SETOF text
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_skeys$function$
;

-- DROP FUNCTION public.slice(hstore, _text);

CREATE OR REPLACE FUNCTION public.slice(hstore, text[])
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_slice_to_hstore$function$
;

-- DROP FUNCTION public.slice_array(hstore, _text);

CREATE OR REPLACE FUNCTION public.slice_array(hstore, text[])
 RETURNS text[]
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_slice_to_array$function$
;

-- DROP FUNCTION public.state_update(int4, int4);

CREATE OR REPLACE FUNCTION public.state_update(arg_vmid integer, arg_state integer)
 RETURNS integer
 LANGUAGE plpgsql
AS $function$
DECLARE
    old_state int4 = NULL;
BEGIN
    SELECT
        state INTO old_state
    FROM
        state
    WHERE
        vmid = arg_vmid
    LIMIT 1
    FOR UPDATE;
    INSERT INTO state (vmid, state, received)
        VALUES (arg_vmid, arg_state, CURRENT_TIMESTAMP)
    ON CONFLICT (vmid)
        DO UPDATE SET
            state = excluded.state, received = excluded.received;
    RETURN old_state;
END;
$function$
;

-- DROP FUNCTION public.svals(hstore);

CREATE OR REPLACE FUNCTION public.svals(hstore)
 RETURNS SETOF text
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE STRICT
AS '$libdir/hstore', $function$hstore_svals$function$
;

-- DROP FUNCTION public.tax_job_maint_after();

CREATE OR REPLACE FUNCTION public.tax_job_maint_after()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
    CASE new.state
    WHEN 'final' THEN
        NOTIFY tax_job_final;
    WHEN 'help' THEN
        NOTIFY tax_job_help;
    WHEN 'sched' THEN
        NOTIFY tax_job_sched;
    ELSE
        NULL;
    END CASE;
    RETURN NEW;
END;
$function$
;

-- DROP FUNCTION public.tax_job_maint_before();

CREATE OR REPLACE FUNCTION public.tax_job_maint_before()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
    IF new.state = 'final' THEN
        new.scheduled = NULL;
    END IF;
    RETURN NEW;
END;
$function$
;

-- DROP FUNCTION public.tax_job_modified();

CREATE OR REPLACE FUNCTION public.tax_job_modified()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
    new.modified := CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$function$
;

-- DROP FUNCTION public.tax_job_take(text);

CREATE OR REPLACE FUNCTION public.tax_job_take(arg_worker text)
 RETURNS SETOF tax_job
 LANGUAGE sql
AS $function$
    UPDATE
        tax_job
    SET
        state = 'busy',
        worker = arg_worker
    WHERE
        state = 'sched'
        AND scheduled <= CURRENT_TIMESTAMP
        AND id = (
            SELECT
                id
            FROM
                tax_job
            WHERE
                state = 'sched'
                AND scheduled <= CURRENT_TIMESTAMP
            ORDER BY
                scheduled,
                modified
            LIMIT 1
            FOR UPDATE
                SKIP LOCKED)
    RETURNING
        *;

$function$
;

-- DROP FUNCTION public.tax_job_trans(trans);

CREATE OR REPLACE FUNCTION public.tax_job_trans(t trans)
 RETURNS tax_job
 LANGUAGE plpgsql
AS $function$
    # print_strict_params ON
DECLARE
    tjd jsonb;
    ops jsonb;
    tj tax_job;
    name text;
BEGIN
    -- lock trans row
    PERFORM
        1
    FROM
        trans
    WHERE (vmid, vmtime) = (t.vmid,
        t.vmtime)
LIMIT 1
FOR UPDATE;
    -- if trans already has tax_job assigned, just return it
    IF t.tax_job_id IS NOT NULL THEN
        SELECT
            * INTO STRICT tj
        FROM
            tax_job
        WHERE
            id = t.tax_job_id;
        RETURN tj;
    END IF;
    -- op code to human friendly name via catalog
    SELECT
        catalog.name INTO name
    FROM
        catalog
    WHERE (vmid, code) = (t.vmid,
        t.menu_code);
    IF NOT found THEN
        name := '#' || t.menu_code;
    END IF;
    ops := jsonb_build_array (jsonb_build_object('vmid', t.vmid, 'time', t.vmtime, 'name', name, 'code', t.menu_code, 'amount', 1, 'price', t.price, 'method', t.method));
    INSERT INTO tax_job (state, created, modified, scheduled, processor, ops, gross)
        VALUES ('sched', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'ru2019', ops, t.price)
    RETURNING
        * INTO STRICT tj;
    UPDATE
        trans
    SET
        tax_job_id = tj.id
    WHERE (vmid, vmtime) = (t.vmid,
        t.vmtime);
    RETURN tj;
END;
$function$
;

-- DROP FUNCTION public.tconvert(text, text);

CREATE OR REPLACE FUNCTION public.tconvert(text, text)
 RETURNS hstore
 LANGUAGE c
 IMMUTABLE PARALLEL SAFE
AS '$libdir/hstore', $function$hstore_from_text$function$
;

-- DROP FUNCTION public.trans_tax_trigger();

CREATE OR REPLACE FUNCTION public.trans_tax_trigger()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
	IF (NEW.vmid = (SELECT vmid from robot where robot.vmid = NEW.vmid and robot.work = TRUE) and (NEW.method = 1 or NEW.method = 2)) THEN
	    PERFORM
       	tax_job_trans (new);
    END IF;
    RETURN new;
END;
$function$
;

-- DROP FUNCTION public.vmstate(int4);

CREATE OR REPLACE FUNCTION public.vmstate(s integer)
 RETURNS text
 LANGUAGE sql
 IMMUTABLE STRICT
AS $function$
    -- TODO generate from tele.proto
    -- Invalid = 0;
    -- Boot = 1;
    -- Nominal = 2;
    -- Disconnected = 3;
    -- Problem = 4;
    -- Service = 5;
    -- Lock = 6;
    SELECT
        CASE WHEN s = 0 THEN
            'Invalid'
        WHEN s = 1 THEN
            'Boot'
        WHEN s = 2 THEN
            'Nominal'
        WHEN s = 3 THEN
            'Disconnected'
        WHEN s = 4 THEN
            'Problem'
        WHEN s = 5 THEN
            'Service'
        WHEN s = 6 THEN
            'Lock'
        ELSE
            'unknown:' || s
        END
$function$
;