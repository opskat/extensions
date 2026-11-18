--------------------------------------------------------
--  File created - Wednesday-November-18-2026   
--------------------------------------------------------
REM INSERTING into OPSKAT_SCRIPT_T
SET DEFINE OFF;
Insert into OPSKAT_SCRIPT_T (ID,NAME,NOTE,CREATED) values ('1','AT&T','it''s; fine',to_date('2026-11-18 10:11:12','YYYY-MM-DD HH24:MI:SS'));
Insert into OPSKAT_SCRIPT_T (ID,NAME,NOTE,CREATED) values ('2','中文 名字','line one
/
line three',null);
Insert into OPSKAT_SCRIPT_T (ID,NAME,NOTE,CREATED) values ('3','q',q'[a;b -- not a comment]',null);
PROMPT it's done with the rows
BEGIN
  UPDATE OPSKAT_SCRIPT_T SET NOTE = NOTE || ' (checked)' WHERE ID = 1;
  INSERT INTO OPSKAT_SCRIPT_T (ID, NAME) VALUES (4, 'from a block');
END;
/
EXEC INSERT INTO OPSKAT_SCRIPT_T (ID, NAME) VALUES (5, 'from exec')
COMMIT;
