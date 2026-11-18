-- OpsKat SQL Dump
--
-- OpsKat version: 1.15.1
-- Asset: hr-db
-- Database: HR
-- Content: structure and data
-- Options: views, functions and procedures, triggers, drop statements
-- Date: 2026-11-18 10:00:00 +00:00
--
-- File encoding: UTF-8

-- ----------------------------
-- Drop existing objects
-- ----------------------------
BEGIN EXECUTE IMMEDIATE 'DROP TRIGGER "TRG_STMT"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-4080) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP TRIGGER "TRG_BI"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-4080) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP VIEW "V_EXP"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-942) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP VIEW "V_ITEMS"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-942) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP PROCEDURE "P_RUN"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-4043) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP PACKAGE "UTIL_PKG"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-4043) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP FUNCTION "F_TAX"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-4043) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP TABLE "ITEMS"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-942) THEN RAISE; END IF; END;
/
BEGIN EXECUTE IMMEDIATE 'DROP SEQUENCE "ITEM_SEQ"'; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (-2289) THEN RAISE; END IF; END;
/

CREATE SEQUENCE "ITEM_SEQ" MINVALUE 1 MAXVALUE 9999999999999999999999999999 INCREMENT BY 1 START WITH 1 CACHE 20 NOORDER NOCYCLE;

-- ----------------------------
-- Table structure for ITEMS
-- ----------------------------
CREATE TABLE "ITEMS" ("ID" NUMBER PRIMARY KEY, "PRICE" NUMBER);

-- ----------------------------
-- Records of ITEMS
-- ----------------------------
INSERT INTO "ITEMS" ("ID", "PRICE") VALUES (1, 12.5);

-- ----------------------------
-- Function structure for F_TAX
-- ----------------------------
CREATE OR REPLACE EDITIONABLE FUNCTION "F_TAX" (p NUMBER) RETURN NUMBER IS
BEGIN
  RETURN p * 0.1;
END;
/

-- ----------------------------
-- Procedure structure for UTIL_PKG (package)
-- ----------------------------
CREATE OR REPLACE EDITIONABLE PACKAGE "UTIL_PKG" AS
  PROCEDURE log(msg VARCHAR2);
END util_pkg;
/
CREATE OR REPLACE EDITIONABLE PACKAGE BODY "UTIL_PKG" AS
  PROCEDURE log(msg VARCHAR2) IS BEGIN NULL; END;
END util_pkg;
/

-- ----------------------------
-- Procedure structure for P_RUN
-- ----------------------------
CREATE OR REPLACE EDITIONABLE PROCEDURE "P_RUN" AS
BEGIN
  util_pkg.log('run'); -- HR.UTIL_PKG
END;
/

-- ----------------------------
-- View structure for V_ITEMS
-- ----------------------------
CREATE OR REPLACE FORCE EDITIONABLE VIEW "V_ITEMS" ("ID", "PRICE", "TAX") AS 
  SELECT id, price, f_tax(price) tax FROM items;

-- ----------------------------
-- View structure for V_EXP
-- ----------------------------
CREATE OR REPLACE FORCE EDITIONABLE VIEW "V_EXP" ("ID", "TAX") AS 
  SELECT hr.id, tax FROM v_items hr WHERE price > 10;

-- ----------------------------
-- Trigger structure for TRG_BI
-- ----------------------------
CREATE OR REPLACE EDITIONABLE TRIGGER "TRG_BI" BEFORE INSERT ON ITEMS FOR EACH ROW
BEGIN
  IF :new.id IS NULL THEN :new.id := item_seq.nextval; END IF;
  INSERT INTO ref.audit_log VALUES ('ON HR.ITEMS');
END;
/
ALTER TRIGGER "TRG_BI" ENABLE;

-- ----------------------------
-- Trigger structure for TRG_STMT
-- ----------------------------
CREATE OR REPLACE EDITIONABLE TRIGGER "TRG_STMT" after update on items
begin
  null;
end;
/
ALTER TRIGGER "TRG_STMT" DISABLE;
